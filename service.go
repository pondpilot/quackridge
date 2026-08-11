package quackridge

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"sync"
	"time"
)

// State is the externally visible lifecycle state.
type State string

const (
	StateStopped   State = "stopped"
	StateStarting  State = "starting"
	StateReady     State = "ready"
	StateDegraded  State = "degraded"
	StateReloading State = "reloading"
	StateStopping  State = "stopping"
	StateFailed    State = "failed"
)

type SourceStatus struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Type      string `json:"type"`
	Health    string `json:"health"`
	ErrorCode string `json:"error_code,omitempty"`
}

// Status is an immutable snapshot. Returned slices are cloned by Status.
type Status struct {
	State        State          `json:"state"`
	Endpoint     string         `json:"endpoint,omitempty"`
	StartedAt    time.Time      `json:"started_at,omitempty"`
	Sources      []SourceStatus `json:"sources"`
	LastError    string         `json:"last_error,omitempty"`
	Capabilities []string       `json:"capabilities"`
}

type Options struct {
	ExtensionDir string
	ListenHost   string
	ListenPort   int
	Token        string
	MemoryLimit  string
	TempLimit    string
	Threads      int
	QueryTimeout time.Duration
	Logger       *slog.Logger
}

// Runtime is the narrow internal engine contract consumed by the facade.
type Runtime interface {
	Start(context.Context, Options) (endpoint string, err error)
	Reload(context.Context) error
	Stop(context.Context) error
	Sources() []SourceStatus
}

type Service struct {
	mu      sync.RWMutex
	runtime Runtime
	status  Status
	opMu    sync.Mutex
}

func New(runtime Runtime) *Service {
	return &Service{runtime: runtime, status: Status{State: StateStopped, Capabilities: Capabilities()}}
}

func (s *Service) Start(ctx context.Context, opts Options) error {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	s.mu.Lock()
	if s.status.State != StateStopped && s.status.State != StateFailed {
		s.mu.Unlock()
		return &Error{Code: CodeInternal, Message: "service is already active"}
	}
	s.status.State = StateStarting
	s.status.LastError = ""
	s.mu.Unlock()

	endpoint, err := s.runtime.Start(ctx, opts)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		s.status.State = StateFailed
		s.status.LastError = sanitize(err)
		return err
	}
	s.status.State = StateReady
	s.status.Endpoint = endpoint
	s.status.StartedAt = time.Now().UTC()
	s.status.Sources = slices.Clone(s.runtime.Sources())
	if hasUnavailableSource(s.status.Sources) {
		s.status.State = StateDegraded
	}
	return nil
}

func (s *Service) Reload(ctx context.Context) error {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	s.mu.Lock()
	previous := s.status.State
	if previous != StateReady && previous != StateDegraded {
		s.mu.Unlock()
		return &Error{Code: CodeInternal, Message: "service is not running"}
	}
	s.status.State = StateReloading
	s.mu.Unlock()

	err := s.runtime.Reload(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	// Reload is transactional: retain the previous healthy state on failure.
	s.status.State = previous
	if err != nil {
		s.status.LastError = sanitize(err)
		return err
	}
	s.status.LastError = ""
	s.status.Sources = slices.Clone(s.runtime.Sources())
	if hasUnavailableSource(s.status.Sources) {
		s.status.State = StateDegraded
	} else {
		s.status.State = StateReady
	}
	return nil
}

func (s *Service) Stop(ctx context.Context) error {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	s.mu.Lock()
	if s.status.State == StateStopped {
		s.mu.Unlock()
		return nil
	}
	s.status.State = StateStopping
	s.mu.Unlock()

	err := s.runtime.Stop(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		s.status.State = StateFailed
		s.status.LastError = sanitize(err)
		return err
	}
	s.status = Status{State: StateStopped, Capabilities: Capabilities()}
	return nil
}

func hasUnavailableSource(sources []SourceStatus) bool {
	for _, source := range sources {
		if source.Health != "ready" {
			return true
		}
	}
	return false
}

func (s *Service) Status() Status {
	s.mu.RLock()
	status := s.status
	s.mu.RUnlock()
	if status.State == StateReady || status.State == StateDegraded {
		status.Sources = slices.Clone(s.runtime.Sources())
		if hasUnavailableSource(status.Sources) {
			status.State = StateDegraded
		} else {
			status.State = StateReady
		}
	}
	status.Sources = slices.Clone(status.Sources)
	status.Capabilities = slices.Clone(status.Capabilities)
	return status
}

func sanitize(err error) string {
	var public *Error
	if errors.As(err, &public) {
		return public.Error()
	}
	return (&Error{Code: CodeInternal, Message: "internal failure"}).Error()
}
