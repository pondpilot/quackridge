package quackridge

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type fakeRuntime struct {
	mu        sync.Mutex
	reloadErr error
	sources   []SourceStatus
}

func (f *fakeRuntime) Start(context.Context, Options) (string, error) {
	return "quack:127.0.0.1:9494", nil
}
func (f *fakeRuntime) Reload(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reloadErr
}
func (f *fakeRuntime) Stop(context.Context) error { return nil }
func (f *fakeRuntime) Sources() []SourceStatus {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]SourceStatus(nil), f.sources...)
}

func TestLifecycle(t *testing.T) {
	runtime := &fakeRuntime{}
	service := New(runtime)
	if got := service.Status().State; got != StateStopped {
		t.Fatalf("initial state = %s", got)
	}
	if err := service.Start(t.Context(), Options{}); err != nil {
		t.Fatal(err)
	}
	if got := service.Status().State; got != StateReady {
		t.Fatalf("started state = %s", got)
	}
	runtime.mu.Lock()
	runtime.reloadErr = errors.New("credential=secret")
	runtime.mu.Unlock()
	if err := service.Reload(t.Context()); err == nil {
		t.Fatal("expected reload failure")
	}
	status := service.Status()
	if status.State != StateReady || status.LastError != "QR_INTERNAL: internal failure" {
		t.Fatalf("transactional reload status: %#v", status)
	}
	if err := service.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := service.Status().State; got != StateStopped {
		t.Fatalf("stopped state = %s", got)
	}
}

func TestStatusSnapshotsAreImmutableAndReflectSourceHealth(t *testing.T) {
	runtime := &fakeRuntime{sources: []SourceStatus{{
		ID: "warehouse", Name: "Warehouse", Type: "postgres", Health: "unavailable",
		ErrorCode: string(CodeSourceUnavailable),
	}}}
	service := New(runtime)
	if err := service.Start(t.Context(), Options{}); err != nil {
		t.Fatal(err)
	}
	status := service.Status()
	if status.State != StateDegraded || len(status.Sources) != 1 {
		t.Fatalf("degraded status = %#v", status)
	}
	status.Capabilities[0] = "mutated"
	status.Sources[0].Name = "mutated"
	next := service.Status()
	if next.Capabilities[0] == "mutated" || next.Sources[0].Name == "mutated" {
		t.Fatalf("caller mutated service state: %#v", next)
	}

	runtime.mu.Lock()
	runtime.sources[0].Health = "ready"
	runtime.sources[0].ErrorCode = ""
	runtime.mu.Unlock()
	if got := service.Status().State; got != StateReady {
		t.Fatalf("recovered state = %s", got)
	}
}

func TestConcurrentStatusIsSafe(t *testing.T) {
	service := New(&fakeRuntime{})
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				_ = service.Status()
			}
		}()
	}
	wg.Wait()
}

type controlledRuntime struct {
	startEntered  chan struct{}
	startRelease  chan struct{}
	reloadEntered chan struct{}
	reloadRelease chan struct{}
	stopEntered   chan struct{}
	stopRelease   chan struct{}
}

func newControlledRuntime() *controlledRuntime {
	return &controlledRuntime{
		startEntered: make(chan struct{}), startRelease: make(chan struct{}),
		reloadEntered: make(chan struct{}), reloadRelease: make(chan struct{}),
		stopEntered: make(chan struct{}), stopRelease: make(chan struct{}),
	}
}

func (r *controlledRuntime) Start(context.Context, Options) (string, error) {
	close(r.startEntered)
	<-r.startRelease
	return "quack:127.0.0.1:9494", nil
}
func (r *controlledRuntime) Reload(context.Context) error {
	close(r.reloadEntered)
	<-r.reloadRelease
	return nil
}
func (r *controlledRuntime) Stop(context.Context) error {
	close(r.stopEntered)
	<-r.stopRelease
	return nil
}
func (*controlledRuntime) Sources() []SourceStatus { return nil }

func TestObservableLifecycleTransitions(t *testing.T) {
	runtime := newControlledRuntime()
	service := New(runtime)
	startDone := make(chan error, 1)
	go func() { startDone <- service.Start(t.Context(), Options{}) }()
	awaitSignal(t, runtime.startEntered)
	if got := service.Status().State; got != StateStarting {
		t.Fatalf("starting state = %s", got)
	}
	close(runtime.startRelease)
	if err := <-startDone; err != nil {
		t.Fatal(err)
	}

	reloadDone := make(chan error, 1)
	go func() { reloadDone <- service.Reload(t.Context()) }()
	awaitSignal(t, runtime.reloadEntered)
	if got := service.Status().State; got != StateReloading {
		t.Fatalf("reloading state = %s", got)
	}
	close(runtime.reloadRelease)
	if err := <-reloadDone; err != nil {
		t.Fatal(err)
	}

	stopDone := make(chan error, 1)
	go func() { stopDone <- service.Stop(t.Context()) }()
	awaitSignal(t, runtime.stopEntered)
	if got := service.Status().State; got != StateStopping {
		t.Fatalf("stopping state = %s", got)
	}
	close(runtime.stopRelease)
	if err := <-stopDone; err != nil {
		t.Fatal(err)
	}
}

func awaitSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatal("runtime operation did not start")
	}
}
