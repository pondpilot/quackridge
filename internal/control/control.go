// Package control implements the versioned local-user management protocol.
package control

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"sync"
	"time"

	quackridge "github.com/pondpilot/quackridge"
	"github.com/pondpilot/quackridge/internal/config"
)

const Version = 1

type Backend interface {
	Status() quackridge.Status
	Reload(context.Context) error
}

type Request struct {
	Version    int      `json:"version"`
	Operation  string   `json:"operation"`
	Origins    []string `json:"origins,omitempty"`
	TTLSeconds int      `json:"ttl_seconds,omitempty"`
}

type PairingChallenge struct {
	URL       string    `json:"url"`
	Nonce     string    `json:"nonce"`
	ExpiresAt time.Time `json:"expires_at"`
}

type Response struct {
	Version       int                  `json:"version"`
	OK            bool                 `json:"ok"`
	Status        *quackridge.Status   `json:"status,omitempty"`
	ErrorCode     quackridge.ErrorCode `json:"error_code,omitempty"`
	Message       string               `json:"message,omitempty"`
	Pairing       *PairingChallenge    `json:"pairing,omitempty"`
	Configuration *config.Document     `json:"configuration,omitempty"`
	Diagnostics   map[string]any       `json:"diagnostics,omitempty"`
	VersionInfo   map[string]any       `json:"version_info,omitempty"`
}

type PairingBackend interface {
	Pair(context.Context, []string, time.Duration) (PairingChallenge, error)
	RotateToken(context.Context) error
}

type ManagementBackend interface {
	Configuration() (config.Document, error)
	Diagnostics(context.Context) (map[string]any, error)
}

type Server struct {
	listener net.Listener
	backend  Backend
	done     chan struct{}
	once     sync.Once
	wg       sync.WaitGroup
}

func Start(address string, backend Backend) (*Server, error) {
	if backend == nil {
		return nil, errors.New("control backend is required")
	}
	listener, err := listen(address)
	if err != nil {
		return nil, err
	}
	server := &Server{listener: listener, backend: backend, done: make(chan struct{})}
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
	_ = connection.SetDeadline(time.Now().Add(5 * time.Second))
	reader := io.LimitReader(connection, 64<<10)
	decoder := json.NewDecoder(bufio.NewReader(reader))
	decoder.DisallowUnknownFields()
	var request Request
	if err := decoder.Decode(&request); err != nil {
		_ = json.NewEncoder(connection).Encode(failure(quackridge.CodeProtocolMismatch, "malformed control request"))
		return
	}
	response := s.dispatch(request)
	_ = json.NewEncoder(connection).Encode(response)
}

func (s *Server) dispatch(request Request) Response {
	if request.Version != Version {
		return failure(quackridge.CodeProtocolMismatch, "unsupported control protocol")
	}
	switch request.Operation {
	case "status":
		status := s.backend.Status()
		return Response{Version: Version, OK: true, Status: &status}
	case "reload":
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := s.backend.Reload(ctx); err != nil {
			classified := quackridge.ClassifyError(err)
			var public *quackridge.Error
			_ = errors.As(classified, &public)
			return failure(public.Code, public.Message)
		}
		status := s.backend.Status()
		return Response{Version: Version, OK: true, Status: &status}
	case "pair":
		backend, ok := s.backend.(PairingBackend)
		if !ok {
			return failure(quackridge.CodeInternal, "pairing is unavailable")
		}
		challenge, err := backend.Pair(context.Background(), request.Origins, time.Duration(request.TTLSeconds)*time.Second)
		if err != nil {
			return failure(quackridge.CodeInternal, "pairing failed")
		}
		return Response{Version: Version, OK: true, Pairing: &challenge}
	case "rotate_token":
		backend, ok := s.backend.(PairingBackend)
		if !ok {
			return failure(quackridge.CodeInternal, "token rotation is unavailable")
		}
		if err := backend.RotateToken(context.Background()); err != nil {
			return failure(quackridge.CodeInternal, "token rotation failed")
		}
		status := s.backend.Status()
		return Response{Version: Version, OK: true, Status: &status}
	case "configuration":
		backend, ok := s.backend.(ManagementBackend)
		if !ok {
			return failure(quackridge.CodeInternal, "configuration is unavailable")
		}
		document, err := backend.Configuration()
		if err != nil {
			return failure(quackridge.CodeInternal, "configuration is unavailable")
		}
		return Response{Version: Version, OK: true, Configuration: &document}
	case "diagnostics":
		backend, ok := s.backend.(ManagementBackend)
		if !ok {
			return failure(quackridge.CodeInternal, "diagnostics are unavailable")
		}
		diagnostics, err := backend.Diagnostics(context.Background())
		if err != nil {
			return failure(quackridge.CodeInternal, "diagnostics are unavailable")
		}
		return Response{Version: Version, OK: true, Diagnostics: diagnostics}
	case "version":
		return Response{Version: Version, OK: true, VersionInfo: map[string]any{
			"product": quackridge.Product, "product_version": quackridge.Version,
			"protocol_version": quackridge.ProtocolVersion, "capabilities": quackridge.Capabilities(),
		}}
	default:
		return failure(quackridge.CodeProtocolMismatch, "unknown control operation")
	}
}

func failure(code quackridge.ErrorCode, message string) Response {
	return Response{Version: Version, OK: false, ErrorCode: code, Message: message}
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
	connection, err := dial(ctx, address)
	if err != nil {
		return Response{}, err
	}
	defer connection.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = connection.SetDeadline(deadline)
	}
	if err := json.NewEncoder(connection).Encode(request); err != nil {
		return Response{}, err
	}
	var response Response
	if err := json.NewDecoder(io.LimitReader(connection, 1<<20)).Decode(&response); err != nil {
		return Response{}, err
	}
	return response, nil
}
