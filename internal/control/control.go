// Package control implements the versioned local-user management protocol.
package control

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"sync"
	"time"

	quackridge "github.com/pondpilot/quackridge"
	"github.com/pondpilot/quackridge/internal/certstore"
	"github.com/pondpilot/quackridge/internal/config"
)

const (
	Version           = 2
	maxFrameSize      = 64 << 10
	shortOperation    = 5 * time.Second
	mutationOperation = 30 * time.Second
)

type Backend interface {
	Status() quackridge.Status
	Reload(context.Context) error
}

type Request struct {
	Version    int             `json:"version"`
	RequestID  string          `json:"request_id"`
	Operation  string          `json:"operation"`
	Payload    json.RawMessage `json:"payload,omitempty"`
	Origins    []string        `json:"origins,omitempty"`
	TTLSeconds int             `json:"ttl_seconds,omitempty"`
}

type ErrorObject struct {
	Code     quackridge.ErrorCode `json:"code"`
	Message  string               `json:"message"`
	Field    string               `json:"field,omitempty"`
	Recovery map[string]string    `json:"recovery,omitempty"`
}

type PairingChallenge struct {
	ID        string    `json:"id"`
	URL       string    `json:"url"`
	Nonce     string    `json:"nonce"`
	ExpiresAt time.Time `json:"expires_at"`
}

type PairingState struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

type ManualRevealPreparation struct {
	Nonce     string    `json:"nonce"`
	ExpiresAt time.Time `json:"expires_at"`
}

type SensitiveToken struct {
	Token string `json:"token"`
}

type PairingStartPayload struct {
	Origins    []string `json:"origins"`
	TTLSeconds int      `json:"ttl_seconds"`
}

type Response struct {
	Version           int                      `json:"version"`
	RequestID         string                   `json:"request_id"`
	OK                bool                     `json:"ok"`
	Error             *ErrorObject             `json:"error,omitempty"`
	Status            *quackridge.Status       `json:"status,omitempty"`
	ErrorCode         quackridge.ErrorCode     `json:"error_code,omitempty"`
	Message           string                   `json:"message,omitempty"`
	Pairing           *PairingChallenge        `json:"pairing,omitempty"`
	PairingState      *PairingState            `json:"pairing_state,omitempty"`
	ManualReveal      *ManualRevealPreparation `json:"manual_reveal,omitempty"`
	Sensitive         *SensitiveToken          `json:"sensitive,omitempty"`
	Certificates      []certstore.Certificate  `json:"certificates,omitempty"`
	Configuration     *config.Document         `json:"configuration,omitempty"`
	Revision          string                   `json:"revision,omitempty"`
	Diagnostics       map[string]any           `json:"diagnostics,omitempty"`
	VersionInfo       map[string]any           `json:"version_info,omitempty"`
	DaemonInstanceID  string                   `json:"daemon_instance_id,omitempty"`
	PairingGeneration string                   `json:"pairing_generation,omitempty"`
}

type PairingBackend interface {
	Pair(context.Context, []string, time.Duration) (PairingChallenge, error)
	RotateToken(context.Context) error
}

type PairingLifecycleBackend interface {
	PairingStatus(string) (string, bool)
	CancelPairing(string) bool
}

type ManualRevealBackend interface {
	PrepareManualReveal(context.Context) (ManualRevealPreparation, error)
	ConsumeManualReveal(context.Context, string, string) (string, error)
}

type CertificateBackend interface {
	ImportCertificate([]byte) (certstore.Certificate, error)
	ListCertificates() ([]certstore.Certificate, error)
	RemoveCertificate(string) error
}

type ManagementBackend interface {
	Configuration() (config.Document, error)
	Diagnostics(context.Context) (map[string]any, error)
}

type SourceManagementBackend interface {
	MutateSource(context.Context, config.Mutation) (config.Document, string, error)
}

type SourceHealthBackend interface {
	RefreshSourceHealth(context.Context, string) error
}

type Server struct {
	listener          net.Listener
	backend           Backend
	done              chan struct{}
	once              sync.Once
	wg                sync.WaitGroup
	daemonInstanceID  string
	pairingGeneration string
	identityMu        sync.RWMutex
}

func (s *Server) Identity() (string, string) {
	s.identityMu.RLock()
	defer s.identityMu.RUnlock()
	return s.daemonInstanceID, s.pairingGeneration
}

func Start(address string, backend Backend) (*Server, error) {
	if backend == nil {
		return nil, errors.New("control backend is required")
	}
	listener, err := listen(address)
	if err != nil {
		return nil, err
	}
	daemonID, err := newID()
	if err != nil {
		_ = listener.Close()
		return nil, err
	}
	pairingID, err := newID()
	if err != nil {
		_ = listener.Close()
		return nil, err
	}
	server := &Server{listener: listener, backend: backend, done: make(chan struct{}), daemonInstanceID: daemonID, pairingGeneration: pairingID}
	server.wg.Add(1)
	go server.serve()
	return server, nil
}

func (s *Server) serve() {
	defer s.wg.Done()
	for {
		connection, err := s.listener.Accept()
		if err != nil {
			select {
			case <-s.done:
				return
			default:
				continue
			}
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			defer connection.Close()
			s.handle(connection)
		}()
	}
}

func (s *Server) handle(connection net.Conn) {
	_ = connection.SetReadDeadline(time.Now().Add(shortOperation))
	reader := bufio.NewReader(io.LimitReader(connection, maxFrameSize+1))
	frame, err := reader.ReadBytes('\n')
	if err != nil || len(frame) > maxFrameSize {
		s.writeResponse(connection, failure("", quackridge.CodeProtocolMismatch, "malformed control request"))
		return
	}
	decoder := json.NewDecoder(bytes.NewReader(frame))
	decoder.DisallowUnknownFields()
	var request Request
	if err := decoder.Decode(&request); err != nil {
		s.writeResponse(connection, failure("", quackridge.CodeProtocolMismatch, "malformed control request"))
		return
	}
	if err := ensureEOF(decoder); err != nil {
		s.writeResponse(connection, failure(request.RequestID, quackridge.CodeProtocolMismatch, "malformed control request"))
		return
	}
	_ = connection.SetDeadline(time.Now().Add(operationTimeout(request.Operation) + time.Second))
	response := s.dispatch(request)
	s.writeResponse(connection, response)
}

func (s *Server) writeResponse(connection net.Conn, response Response) {
	encoded, err := json.Marshal(response)
	if err != nil || len(encoded)+1 > maxFrameSize {
		encoded, _ = json.Marshal(failure(response.RequestID, quackridge.CodeInternal, "control response is unavailable"))
	}
	encoded = append(encoded, '\n')
	_, _ = connection.Write(encoded)
}

func operationTimeout(operation string) time.Duration {
	if operation == "reload" || operation == "service_reload" || len(operation) > len("source_") && operation[:len("source_")] == "source_" {
		return mutationOperation
	}
	return shortOperation
}

func (s *Server) dispatch(request Request) Response {
	if request.Version != Version {
		return failure(request.RequestID, quackridge.CodeIncompatible, "unsupported management protocol")
	}
	if request.RequestID == "" || len(request.RequestID) > 128 {
		return failure(request.RequestID, quackridge.CodeProtocolMismatch, "request_id is required")
	}
	success := func(response Response) Response {
		response.Version, response.RequestID, response.OK = Version, request.RequestID, true
		s.identityMu.RLock()
		response.DaemonInstanceID, response.PairingGeneration = s.daemonInstanceID, s.pairingGeneration
		s.identityMu.RUnlock()
		return response
	}
	switch request.Operation {
	case "handshake":
		return success(Response{VersionInfo: map[string]any{
			"product": quackridge.Product, "product_version": quackridge.Version,
			"management_protocol_version": Version, "quack_protocol_version": quackridge.ProtocolVersion,
			"capabilities": quackridge.Capabilities(),
		}})
	case "status":
		status := s.backend.Status()
		return success(Response{Status: &status})
	case "reload", "service_reload":
		ctx, cancel := context.WithTimeout(context.Background(), mutationOperation)
		defer cancel()
		if err := s.backend.Reload(ctx); err != nil {
			classified := quackridge.ClassifyError(err)
			var public *quackridge.Error
			_ = errors.As(classified, &public)
			return failure(request.RequestID, public.Code, public.Message)
		}
		status := s.backend.Status()
		return success(Response{Status: &status})
	case "pair", "pair_start":
		backend, ok := s.backend.(PairingBackend)
		if !ok {
			return failure(request.RequestID, quackridge.CodeInternal, "pairing is unavailable")
		}
		origins, ttlSeconds := request.Origins, request.TTLSeconds
		if len(request.Payload) > 0 {
			var payload PairingStartPayload
			if err := decodePayload(request.Payload, &payload); err != nil {
				return failure(request.RequestID, quackridge.CodeValidation, "pairing request is invalid")
			}
			origins, ttlSeconds = payload.Origins, payload.TTLSeconds
		}
		if len(origins) == 0 || ttlSeconds < 1 || ttlSeconds > 600 {
			return failure(request.RequestID, quackridge.CodeValidation, "pairing request is invalid")
		}
		challenge, err := backend.Pair(context.Background(), origins, time.Duration(ttlSeconds)*time.Second)
		if err != nil {
			return failure(request.RequestID, quackridge.CodeInternal, "pairing failed")
		}
		return success(Response{Pairing: &challenge})
	case "rotate_token":
		backend, ok := s.backend.(PairingBackend)
		if !ok {
			return failure(request.RequestID, quackridge.CodeInternal, "token rotation is unavailable")
		}
		if err := backend.RotateToken(context.Background()); err != nil {
			return failure(request.RequestID, quackridge.CodeInternal, "token rotation failed")
		}
		generation, err := newID()
		if err != nil {
			return failure(request.RequestID, quackridge.CodeInternal, "token rotation failed")
		}
		s.identityMu.Lock()
		s.pairingGeneration = generation
		s.identityMu.Unlock()
		status := s.backend.Status()
		return success(Response{Status: &status})
	case "pair_status", "pair_cancel":
		backend, ok := s.backend.(PairingLifecycleBackend)
		if !ok {
			return failure(request.RequestID, quackridge.CodeInternal, "pairing lifecycle is unavailable")
		}
		var payload struct {
			ID string `json:"id"`
		}
		if err := decodePayload(request.Payload, &payload); err != nil || payload.ID == "" {
			return failure(request.RequestID, quackridge.CodeValidation, "pairing request is invalid")
		}
		if request.Operation == "pair_cancel" && !backend.CancelPairing(payload.ID) {
			return failure(request.RequestID, quackridge.CodeValidation, "pairing challenge was not found")
		}
		state, found := backend.PairingStatus(payload.ID)
		if !found {
			return failure(request.RequestID, quackridge.CodeValidation, "pairing challenge was not found")
		}
		return success(Response{PairingState: &PairingState{ID: payload.ID, Status: state}})
	case "manual_reveal_prepare":
		backend, ok := s.backend.(ManualRevealBackend)
		if !ok {
			return failure(request.RequestID, quackridge.CodeInternal, "manual recovery is unavailable")
		}
		prepared, err := backend.PrepareManualReveal(context.Background())
		if err != nil {
			return failure(request.RequestID, quackridge.CodeInternal, "manual recovery is unavailable")
		}
		return success(Response{ManualReveal: &prepared})
	case "manual_reveal_consume":
		backend, ok := s.backend.(ManualRevealBackend)
		if !ok {
			return failure(request.RequestID, quackridge.CodeInternal, "manual recovery is unavailable")
		}
		var payload struct {
			Nonce        string `json:"nonce"`
			Confirmation string `json:"confirmation"`
		}
		if err := decodePayload(request.Payload, &payload); err != nil {
			return failure(request.RequestID, quackridge.CodeValidation, "manual recovery request is invalid")
		}
		token, err := backend.ConsumeManualReveal(context.Background(), payload.Nonce, payload.Confirmation)
		if err != nil {
			return failure(request.RequestID, quackridge.CodeValidation, "manual recovery request is invalid or expired")
		}
		return success(Response{Sensitive: &SensitiveToken{Token: token}})
	case "certificate_import", "certificate_list", "certificate_remove":
		backend, ok := s.backend.(CertificateBackend)
		if !ok {
			return failure(request.RequestID, quackridge.CodeInternal, "certificate management is unavailable")
		}
		if request.Operation == "certificate_import" {
			var payload struct {
				PEM []byte `json:"pem"`
			}
			if err := decodePayload(request.Payload, &payload); err != nil || len(payload.PEM) == 0 {
				return failure(request.RequestID, quackridge.CodeValidation, "certificate is invalid")
			}
			certificate, err := backend.ImportCertificate(payload.PEM)
			if err != nil {
				return failure(request.RequestID, quackridge.CodeValidation, "certificate is invalid")
			}
			return success(Response{Certificates: []certstore.Certificate{certificate}})
		}
		if request.Operation == "certificate_remove" {
			var payload struct {
				Reference string `json:"reference"`
			}
			if err := decodePayload(request.Payload, &payload); err != nil || backend.RemoveCertificate(payload.Reference) != nil {
				return failure(request.RequestID, quackridge.CodeValidation, "certificate cannot be removed")
			}
		}
		certificates, err := backend.ListCertificates()
		if err != nil {
			return failure(request.RequestID, quackridge.CodeInternal, "certificates are unavailable")
		}
		return success(Response{Certificates: certificates})
	case "configuration":
		backend, ok := s.backend.(ManagementBackend)
		if !ok {
			return failure(request.RequestID, quackridge.CodeInternal, "configuration is unavailable")
		}
		document, err := backend.Configuration()
		if err != nil {
			return failure(request.RequestID, quackridge.CodeInternal, "configuration is unavailable")
		}
		revision, err := config.Revision(document)
		if err != nil {
			return failure(request.RequestID, quackridge.CodeInternal, "configuration is unavailable")
		}
		return success(Response{Configuration: &document, Revision: revision})
	case "source_add", "source_test", "source_update", "source_remove", "source_set_enabled":
		backend, ok := s.backend.(SourceManagementBackend)
		if !ok {
			return failure(request.RequestID, quackridge.CodeInternal, "source management is unavailable")
		}
		var mutation config.Mutation
		if err := decodePayload(request.Payload, &mutation); err != nil {
			return failure(request.RequestID, quackridge.CodeValidation, "source mutation is invalid")
		}
		mutation.Operation = request.Operation[len("source_"):]
		ctx, cancel := context.WithTimeout(context.Background(), mutationOperation)
		defer cancel()
		document, revision, err := backend.MutateSource(ctx, mutation)
		if err != nil {
			classified := quackridge.ClassifyError(err)
			var public *quackridge.Error
			_ = errors.As(classified, &public)
			return failure(request.RequestID, public.Code, public.Message)
		}
		return success(Response{Configuration: &document, Revision: revision})
	case "source_refresh":
		backend, ok := s.backend.(SourceHealthBackend)
		if !ok {
			return failure(request.RequestID, quackridge.CodeInternal, "source refresh is unavailable")
		}
		var payload struct {
			ID string `json:"id"`
		}
		if err := decodePayload(request.Payload, &payload); err != nil || payload.ID == "" {
			return failure(request.RequestID, quackridge.CodeValidation, "source refresh request is invalid")
		}
		ctx, cancel := context.WithTimeout(context.Background(), mutationOperation)
		defer cancel()
		if err := backend.RefreshSourceHealth(ctx, payload.ID); err != nil {
			classified := quackridge.ClassifyError(err)
			var public *quackridge.Error
			_ = errors.As(classified, &public)
			return failure(request.RequestID, public.Code, public.Message)
		}
		status := s.backend.Status()
		return success(Response{Status: &status})
	case "diagnostics":
		backend, ok := s.backend.(ManagementBackend)
		if !ok {
			return failure(request.RequestID, quackridge.CodeInternal, "diagnostics are unavailable")
		}
		diagnostics, err := backend.Diagnostics(context.Background())
		if err != nil {
			return failure(request.RequestID, quackridge.CodeInternal, "diagnostics are unavailable")
		}
		return success(Response{Diagnostics: diagnostics})
	case "version":
		return success(Response{VersionInfo: map[string]any{
			"product": quackridge.Product, "product_version": quackridge.Version,
			"protocol_version": quackridge.ProtocolVersion, "capabilities": quackridge.Capabilities(),
		}})
	default:
		return failure(request.RequestID, quackridge.CodeProtocolMismatch, "unknown control operation")
	}
}

func failure(requestID string, code quackridge.ErrorCode, message string) Response {
	return Response{Version: Version, RequestID: requestID, OK: false, Error: &ErrorObject{Code: code, Message: message}, ErrorCode: code, Message: message}
}

func ensureEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("trailing JSON")
	}
	return nil
}

func newID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}

func decodePayload(raw json.RawMessage, target any) error {
	if len(raw) == 0 {
		return errors.New("payload is required")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	return ensureEOF(decoder)
}

func (s *Server) Close() error {
	var err error
	s.once.Do(func() {
		close(s.done)
		err = s.listener.Close()
		s.wg.Wait()
	})
	return err
}

func Call(ctx context.Context, address string, request Request) (Response, error) {
	if request.Version == 0 {
		request.Version = Version
	}
	if request.RequestID == "" {
		requestID, err := newID()
		if err != nil {
			return Response{}, err
		}
		request.RequestID = requestID
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		return Response{}, err
	}
	if len(encoded)+1 > maxFrameSize {
		return Response{}, errors.New("control request exceeds frame limit")
	}
	connection, err := dial(ctx, address)
	if err != nil {
		return Response{}, err
	}
	defer connection.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = connection.SetDeadline(deadline)
	}
	encoded = append(encoded, '\n')
	if _, err := connection.Write(encoded); err != nil {
		return Response{}, err
	}
	reader := bufio.NewReader(io.LimitReader(connection, maxFrameSize+1))
	frame, err := reader.ReadBytes('\n')
	if err != nil || len(frame) > maxFrameSize {
		return Response{}, errors.New("malformed control response")
	}
	var response Response
	decoder := json.NewDecoder(bytes.NewReader(frame))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&response); err != nil {
		return Response{}, err
	}
	if err := ensureEOF(decoder); err != nil {
		return Response{}, err
	}
	if response.RequestID != request.RequestID {
		return Response{}, errors.New("control response request_id mismatch")
	}
	if response.Version != Version {
		return Response{}, errors.New("control response version mismatch")
	}
	return response, nil
}
