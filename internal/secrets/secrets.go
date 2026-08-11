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
