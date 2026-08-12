// Package pairing implements the short-lived browser pairing exchange.
package pairing

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"slices"
	"sync"
	"time"

	protocol "github.com/pondpilot/quackridge/protocol/v1"
)

type Identity = protocol.Identity

type Response struct {
	Endpoint string   `json:"endpoint"`
	Identity Identity `json:"identity"`
	Token    string   `json:"token"`
}

type Options struct {
	Origins  []string
	TTL      time.Duration
	Endpoint string
	Token    string
}

type Challenge struct {
	URL       string
	Nonce     string
	ExpiresAt time.Time
}

type Server struct {
	mu        sync.Mutex
	nonce     string
	expiresAt time.Time
	consumed  bool
	origins   []string
	response  Response
	listener  net.Listener
	http      *http.Server
	done      chan struct{}
	once      sync.Once
}

func Start(options Options) (*Server, Challenge, error) {
	if len(options.Origins) == 0 {
		return nil, Challenge{}, errors.New("at least one pairing origin is required")
	}
	if options.Endpoint == "" || options.Token == "" {
		return nil, Challenge{}, errors.New("pairing endpoint and token are required")
	}
	ttl := options.TTL
	if ttl <= 0 {
		ttl = 2 * time.Minute
	}
	nonce, err := randomNonce()
	if err != nil {
		return nil, Challenge{}, err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, Challenge{}, err
	}
	expiresAt := time.Now().UTC().Add(ttl)
	server := &Server{
		nonce: nonce, expiresAt: expiresAt, origins: slices.Clone(options.Origins), listener: listener,
		done:     make(chan struct{}),
		response: Response{Endpoint: options.Endpoint, Token: options.Token, Identity: protocol.CurrentIdentity()},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/pair", server.handle)
	server.http = &http.Server{
		Handler: mux, ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 2 * time.Second,
		WriteTimeout: 2 * time.Second, IdleTimeout: 2 * time.Second,
	}
	go func() {
		_ = server.http.Serve(listener)
		server.finish()
	}()
	go func() {
		timer := time.NewTimer(ttl)
		defer timer.Stop()
		select {
		case <-timer.C:
			_ = server.Close()
		case <-server.done:
		}
	}()
	challenge := Challenge{
		URL:   "http://" + listener.Addr().String() + "/v1/pair",
		Nonce: nonce, ExpiresAt: expiresAt,
	}
	return server, challenge, nil
}

func (s *Server) handle(writer http.ResponseWriter, request *http.Request) {
	origin := request.Header.Get("Origin")
	if !slices.Contains(s.origins, origin) {
		http.Error(writer, "origin is not allowed", http.StatusForbidden)
		return
	}
	writer.Header().Set("Access-Control-Allow-Origin", origin)
	writer.Header().Set("Vary", "Origin")
	writer.Header().Set("Cache-Control", "no-store")
	if request.Method == http.MethodOptions {
		writer.Header().Set("Access-Control-Allow-Methods", "POST")
		writer.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if request.Header.Get("Access-Control-Request-Private-Network") == "true" {
			writer.Header().Set("Access-Control-Allow-Private-Network", "true")
		}
		writer.WriteHeader(http.StatusNoContent)
		return
	}
	if request.Method != http.MethodPost {
		http.Error(writer, "method is not allowed", http.StatusMethodNotAllowed)
		return
	}
	var submitted struct {
		Nonce string `json:"nonce"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 4<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&submitted); err != nil {
		http.Error(writer, "malformed pairing request", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	switch {
	case s.consumed:
		s.mu.Unlock()
		http.Error(writer, "pairing nonce was already used", http.StatusConflict)
		return
	case time.Now().After(s.expiresAt):
		s.mu.Unlock()
		http.Error(writer, "pairing nonce expired", http.StatusGone)
		return
	case submitted.Nonce != s.nonce:
		s.mu.Unlock()
		http.Error(writer, "pairing nonce is invalid", http.StatusUnauthorized)
		return
	default:
		s.consumed = true
	}
	s.mu.Unlock()
	writer.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(writer).Encode(s.response)
	go s.Close()
}

func (s *Server) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := s.http.Shutdown(ctx)
	s.finish()
	return err
}

func (s *Server) finish()               { s.once.Do(func() { close(s.done) }) }
func (s *Server) Done() <-chan struct{} { return s.done }

func randomNonce() (string, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}
