//go:build !windows

package control

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	quackridge "github.com/pondpilot/quackridge"
	"github.com/pondpilot/quackridge/internal/config"
)

type testBackend struct {
	mu        sync.Mutex
	reloadErr error
}

func (*testBackend) Status() quackridge.Status {
	return quackridge.Status{State: quackridge.StateReady, Endpoint: "quack:127.0.0.1:9494"}
}
func (b *testBackend) Reload(context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.reloadErr
}
func (*testBackend) Configuration() (config.Document, error) {
	return config.Document{Version: config.CurrentVersion, Sources: []config.Source{{
		ID: "warehouse", Type: "postgres", CredentialRef: "quackridge/source/warehouse",
	}}}, nil
}
func (*testBackend) Diagnostics(context.Context) (map[string]any, error) {
	return map[string]any{"source_count": 1, "settings": map[string]string{"lock_configuration": "true"}}, nil
}

func TestControlSocketPermissionsAndConcurrentStatus(t *testing.T) {
	address := filepath.Join(t.TempDir(), "private", "control.sock")
	server, err := Start(address, &testBackend{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	info, err := os.Stat(address)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("socket permissions = %o", info.Mode().Perm())
	}
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			response, err := Call(ctx, address, Request{Version: Version, Operation: "status"})
			if err != nil || !response.OK || response.Status == nil || response.Status.State != quackridge.StateReady {
				t.Errorf("status response = %#v, %v", response, err)
			}
		}()
	}
	wg.Wait()
}

func TestControlRejectsMalformedAndSanitizesReload(t *testing.T) {
	address := filepath.Join(t.TempDir(), "control.sock")
	backend := &testBackend{reloadErr: errors.New("password=secret /private/path")}
	server, err := Start(address, backend)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()

	connection, err := net.Dial("unix", address)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := connection.Write([]byte("{invalid\n")); err != nil {
		t.Fatal(err)
	}
	var malformed Response
	if err := json.NewDecoder(connection).Decode(&malformed); err != nil {
		t.Fatal(err)
	}
	_ = connection.Close()
	if malformed.ErrorCode != quackridge.CodeProtocolMismatch {
		t.Fatalf("malformed response = %#v", malformed)
	}

	response, err := Call(t.Context(), address, Request{Version: Version, Operation: "reload"})
	if err != nil {
		t.Fatal(err)
	}
	if response.OK || response.ErrorCode != quackridge.CodeInternal || response.Message != "query failed" {
		t.Fatalf("reload response = %#v", response)
	}
}

func TestControlManagementResponsesExcludeSecrets(t *testing.T) {
	address := filepath.Join(t.TempDir(), "control.sock")
	server, err := Start(address, &testBackend{})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()

	for _, operation := range []string{"configuration", "diagnostics", "version"} {
		response, err := Call(t.Context(), address, Request{Version: Version, Operation: operation})
		if err != nil || !response.OK {
			t.Fatalf("%s response = %#v, %v", operation, response, err)
		}
		encoded, err := json.Marshal(response)
		if err != nil {
			t.Fatal(err)
		}
		if string(encoded) == "" || strings.Contains(string(encoded), "super-secret-value") {
			t.Fatalf("%s response exposed a secret: %s", operation, encoded)
		}
	}
}
