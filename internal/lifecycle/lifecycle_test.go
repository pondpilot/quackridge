//go:build !windows

package lifecycle

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestEmitterSendsBoundedReadiness(t *testing.T) {
	path := filepath.Join(t.TempDir(), "event.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan Event, 1)
	go func() {
		connection, err := listener.Accept()
		if err != nil {
			return
		}
		defer connection.Close()
		var event Event
		_ = json.NewDecoder(bufio.NewReader(connection)).Decode(&event)
		accepted <- event
	}()
	emitter, err := Connect(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer emitter.Close()
	if err := emitter.Send(Event{Type: "readiness", Readiness: &Readiness{PID: os.Getpid(), DaemonInstanceID: "instance", PairingGeneration: "generation"}}); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-accepted:
		if event.Type != "readiness" || event.Readiness == nil || event.Readiness.PID != os.Getpid() || event.Timestamp.IsZero() {
			t.Fatalf("event = %#v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("readiness was not received")
	}
}

func TestParentContextCancelsOnEOF(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel, err := ParentContext(context.Background(), int(reader.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	_ = writer.Close()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("EOF did not cancel lifecycle context")
	}
}
