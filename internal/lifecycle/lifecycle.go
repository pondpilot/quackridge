// Package lifecycle implements the private app-to-child supervision channel.
package lifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"sync"
	"time"
)

const MaxFrameSize = 64 << 10

type Readiness struct {
	PID                       int    `json:"pid"`
	DaemonInstanceID          string `json:"daemon_instance_id"`
	PairingGeneration         string `json:"pairing_generation"`
	LifecycleState            string `json:"lifecycle_state"`
	Endpoint                  string `json:"endpoint"`
	ControlPath               string `json:"control_path"`
	ProductVersion            string `json:"product_version"`
	ManagementProtocolVersion int    `json:"management_protocol_version"`
}

type Event struct {
	Type      string     `json:"type"`
	Timestamp time.Time  `json:"timestamp"`
	Phase     string     `json:"phase,omitempty"`
	Readiness *Readiness `json:"readiness,omitempty"`
	Code      string     `json:"code,omitempty"`
	Message   string     `json:"message,omitempty"`
}

type Emitter struct {
	mu     sync.Mutex
	writer io.WriteCloser
}

func Connect(ctx context.Context, address string) (*Emitter, error) {
	if address == "" {
		return &Emitter{}, nil
	}
	connection, err := dial(ctx, address)
	if err != nil {
		return nil, err
	}
	return &Emitter{writer: connection}, nil
}

func (e *Emitter) Send(event Event) error {
	if e == nil || e.writer == nil {
		return nil
	}
	event.Timestamp = time.Now().UTC()
	encoded, err := json.Marshal(event)
	if err != nil {
		return err
	}
	if len(encoded)+1 > MaxFrameSize {
		return errors.New("lifecycle frame exceeds limit")
	}
	encoded = append(encoded, '\n')
	e.mu.Lock()
	defer e.mu.Unlock()
	_, err = e.writer.Write(encoded)
	return err
}

func (e *Emitter) Close() error {
	if e == nil || e.writer == nil {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	err := e.writer.Close()
	e.writer = nil
	return err
}

// ParentContext cancels when the inherited lifecycle descriptor reaches EOF.
// A negative descriptor preserves traditional signal-controlled serve mode.
func ParentContext(parent context.Context, descriptor int) (context.Context, context.CancelFunc, error) {
	ctx, cancel := context.WithCancel(parent)
	if descriptor < 0 {
		return ctx, cancel, nil
	}
	file := os.NewFile(uintptr(descriptor), "quackridge-lifecycle")
	if file == nil {
		cancel()
		return nil, nil, errors.New("lifecycle descriptor is invalid")
	}
	go func() {
		defer file.Close()
		var buffer [1]byte
		_, _ = file.Read(buffer[:])
		cancel()
	}()
	return ctx, cancel, nil
}
