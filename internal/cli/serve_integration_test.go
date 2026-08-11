//go:build integration && !windows

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pondpilot/quackridge/internal/control"
	"github.com/pondpilot/quackridge/internal/pairing"
)

const integrationOrigin = "https://app.pondpilot.io"

func TestServeStatusAndBoundedShutdown(t *testing.T) {
	extensionDir := os.Getenv("QUACKRIDGE_EXTENSION_DIR")
	if extensionDir == "" {
		t.Skip("QUACKRIDGE_EXTENSION_DIR is required")
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var stdout, stderr bytes.Buffer
	controlAddress := filepath.Join(t.TempDir(), "control.sock")
	configPath := filepath.Join(t.TempDir(), "config.json")
	application := &App{Stdin: bytes.NewReader(nil), Stdout: &stdout, Stderr: &stderr, Context: ctx}
	done := make(chan int, 1)
	go func() {
		done <- application.Run([]string{
			"serve", "--config", configPath,
			"--extensions", extensionDir, "--control", controlAddress,
			"--credential-provider", "environment", "--json",
		})
	}()
	deadline := time.Now().Add(5 * time.Second)
	ready := false
	for time.Now().Before(deadline) {
		callCtx, callCancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
		response, err := control.Call(callCtx, controlAddress, control.Request{Version: control.Version, Operation: "status"})
		callCancel()
		if err == nil && response.OK {
			ready = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !ready {
		cancel()
		t.Fatalf("serve did not become ready: %s", stderr.String())
	}
	pairCtx, pairCancel := context.WithTimeout(t.Context(), time.Second)
	pairResponse, err := control.Call(pairCtx, controlAddress, control.Request{
		Version: control.Version, Operation: "pair", Origins: []string{integrationOrigin}, TTLSeconds: 1,
	})
	pairCancel()
	if err != nil || !pairResponse.OK || pairResponse.Pairing == nil {
		t.Fatalf("pair control response = %#v, %v", pairResponse, err)
	}
	body, _ := json.Marshal(map[string]string{"nonce": pairResponse.Pairing.Nonce})
	request, _ := http.NewRequest(http.MethodPost, pairResponse.Pairing.URL, bytes.NewReader(body))
	request.Header.Set("Origin", integrationOrigin)
	request.Header.Set("Content-Type", "application/json")
	httpResponse, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	var paired pairing.Response
	if err := json.NewDecoder(httpResponse.Body).Decode(&paired); err != nil {
		_ = httpResponse.Body.Close()
		t.Fatal(err)
	}
	_ = httpResponse.Body.Close()
	if paired.Token == "" || paired.Endpoint == "" {
		t.Fatalf("pairing response = %#v", paired)
	}
	var manualOut, manualErr bytes.Buffer
	manualApp := &App{Stdin: bytes.NewReader(nil), Stdout: &manualOut, Stderr: &manualErr}
	if code := manualApp.Run([]string{"pair", "--control", controlAddress, "--manual", "--json"}); code != 0 {
		t.Fatalf("manual pair exit = %d, stderr=%s", code, manualErr.String())
	}
	var manual pairing.Response
	if err := json.Unmarshal(manualOut.Bytes(), &manual); err != nil {
		t.Fatal(err)
	}
	if manual.Endpoint == "" || manual.Token == "" || manual.Identity.Product == "" {
		t.Fatalf("manual pairing response = %#v", manual)
	}
	for _, operation := range []string{"configuration", "diagnostics", "version"} {
		managementCtx, managementCancel := context.WithTimeout(t.Context(), time.Second)
		response, err := control.Call(managementCtx, controlAddress, control.Request{Version: control.Version, Operation: operation})
		managementCancel()
		if err != nil || !response.OK {
			t.Fatalf("%s response = %#v, %v", operation, response, err)
		}
	}
	rotateCtx, rotateCancel := context.WithTimeout(t.Context(), time.Second)
	rotated, err := control.Call(rotateCtx, controlAddress, control.Request{Version: control.Version, Operation: "rotate_token"})
	rotateCancel()
	if err != nil || !rotated.OK {
		t.Fatalf("rotate response = %#v, %v", rotated, err)
	}
	cancel()
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("serve exit = %d, stderr=%s", code, stderr.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serve did not stop within the shutdown bound")
	}
	if _, err := os.Stat(controlAddress); !os.IsNotExist(err) {
		t.Fatalf("control socket remained after shutdown: %v", err)
	}
}
