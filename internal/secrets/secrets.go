// Package secrets provides explicit credential-store implementations.
package secrets

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
)

var (
	ErrNotFound = errors.New("credential not found")
	ErrReadOnly = errors.New("credential store is read-only")
)

type Store interface {
	Get(context.Context, string) ([]byte, error)
	Put(context.Context, string, []byte) error
	Delete(context.Context, string) error
}

type lazySystem struct {
	once  sync.Once
	store Store
	err   error
}

// NewLazySystemStore defers platform-provider initialization until a source
// actually needs credentials. This lets a file-only daemon accept credentialed
// sources on a later reload without requiring the provider at startup.
func NewLazySystemStore() Store { return &lazySystem{} }

func (s *lazySystem) initialize() (Store, error) {
	s.once.Do(func() { s.store, s.err = NewSystemStore() })
	return s.store, s.err
}

func (s *lazySystem) Get(ctx context.Context, reference string) ([]byte, error) {
	store, err := s.initialize()
	if err != nil {
		return nil, err
	}
	return store.Get(ctx, reference)
}

func (s *lazySystem) Put(ctx context.Context, reference string, value []byte) error {
	store, err := s.initialize()
	if err != nil {
		return err
	}
	return store.Put(ctx, reference, value)
}

func (s *lazySystem) Delete(ctx context.Context, reference string) error {
	store, err := s.initialize()
	if err != nil {
		return err
	}
	return store.Delete(ctx, reference)
}

type Memory struct {
	mu     sync.RWMutex
	values map[string][]byte
}

func NewMemory() *Memory { return &Memory{values: make(map[string][]byte)} }

func (m *Memory) Get(_ context.Context, reference string) ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	value, ok := m.values[reference]
	if !ok {
		return nil, ErrNotFound
	}
	return append([]byte(nil), value...), nil
}

func (m *Memory) Put(_ context.Context, reference string, value []byte) error {
	if reference == "" || len(value) == 0 {
		return errors.New("credential reference and value are required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	clear(m.values[reference])
	m.values[reference] = append([]byte(nil), value...)
	return nil
}

func (m *Memory) Delete(_ context.Context, reference string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	value, ok := m.values[reference]
	if !ok {
		return ErrNotFound
	}
	clear(value)
	delete(m.values, reference)
	return nil
}

// Environment is an explicit CI/headless provider. It never writes values and
// is never selected as an automatic fallback.
type Environment struct{ Prefix string }

func (e Environment) Get(_ context.Context, reference string) ([]byte, error) {
	value, ok := os.LookupEnv(e.key(reference))
	if !ok || value == "" {
		return nil, ErrNotFound
	}
	return []byte(value), nil
}

func (Environment) Put(context.Context, string, []byte) error { return ErrReadOnly }
func (Environment) Delete(context.Context, string) error      { return ErrReadOnly }

func (e Environment) key(reference string) string {
	prefix := e.Prefix
	if prefix == "" {
		prefix = "QUACKRIDGE_SECRET_"
	}
	var key strings.Builder
	key.WriteString(prefix)
	for _, character := range strings.ToUpper(reference) {
		if character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' {
			key.WriteRune(character)
		} else {
			key.WriteByte('_')
		}
	}
	return key.String()
}
