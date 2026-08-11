package quackridge

import (
	"context"
	"errors"
	"sync"
	"testing"
)

type fakeRuntime struct {
	mu        sync.Mutex
	reloadErr error
}

func (f *fakeRuntime) Start(context.Context, Options) (string, error) {
	return "quack:127.0.0.1:9494", nil
}
func (f *fakeRuntime) Reload(context.Context) error { return f.reloadErr }
func (f *fakeRuntime) Stop(context.Context) error   { return nil }

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
	runtime.reloadErr = errors.New("credential=secret")
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
