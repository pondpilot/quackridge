package pairing

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	quackridge "github.com/pondpilot/quackridge"
)

const testOrigin = "https://app.pondpilot.io"

func TestPairingOriginNonceReplayAndShutdown(t *testing.T) {
	server, challenge, err := Start(Options{
		Origins: []string{testOrigin}, TTL: time.Second,
		Endpoint: "quack:127.0.0.1:9494", Token: "01234567890123456789012345678901",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	if len(challenge.Nonce) < 40 {
		t.Fatalf("nonce is too short: %d", len(challenge.Nonce))
	}

	wrongOrigin := pairRequest(t, challenge.URL, challenge.Nonce, "https://evil.example")
	if wrongOrigin.StatusCode != http.StatusForbidden {
		t.Fatalf("wrong origin status = %d", wrongOrigin.StatusCode)
	}
	_ = wrongOrigin.Body.Close()
	wrongNonce := pairRequest(t, challenge.URL, "wrong", testOrigin)
	if wrongNonce.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong nonce status = %d", wrongNonce.StatusCode)
	}
	_ = wrongNonce.Body.Close()

	response := pairRequest(t, challenge.URL, challenge.Nonce, testOrigin)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("pair status = %d", response.StatusCode)
	}
	var paired Response
	if err := json.NewDecoder(response.Body).Decode(&paired); err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if paired.Endpoint != "quack:127.0.0.1:9494" || paired.Token == "" || paired.Identity.Product != quackridge.Product {
		t.Fatalf("paired response = %#v", paired)
	}
	select {
	case <-server.Done():
	case <-time.After(time.Second):
		t.Fatal("pairing server did not stop after success")
	}
}

func TestPairingExpiresAndStops(t *testing.T) {
	server, challenge, err := Start(Options{
		Origins: []string{testOrigin}, TTL: 30 * time.Millisecond,
		Endpoint: "quack:127.0.0.1:9494", Token: "01234567890123456789012345678901",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	select {
	case <-server.Done():
	case <-time.After(time.Second):
		t.Fatal("pairing server did not expire")
	}
	if _, err := http.Get(challenge.URL); err == nil {
		t.Fatal("expired pairing listener remained reachable")
	}
}

func TestPairingRejectsConsumedNonce(t *testing.T) {
	server, challenge, err := Start(Options{
		Origins: []string{testOrigin}, TTL: time.Second,
		Endpoint: "quack:127.0.0.1:9494", Token: "01234567890123456789012345678901",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	server.mu.Lock()
	server.consumed = true
	server.mu.Unlock()
	response := pairRequest(t, challenge.URL, challenge.Nonce, testOrigin)
	defer response.Body.Close()
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("replayed nonce status = %d", response.StatusCode)
	}
}

func TestPairingAllowsPrivateNetworkPreflight(t *testing.T) {
	server, challenge, err := Start(Options{
		Origins: []string{testOrigin}, TTL: time.Second,
		Endpoint: "quack:127.0.0.1:9494", Token: "01234567890123456789012345678901",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()

	request, err := http.NewRequest(http.MethodOptions, challenge.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Origin", testOrigin)
	request.Header.Set("Access-Control-Request-Method", http.MethodPost)
	request.Header.Set("Access-Control-Request-Headers", "content-type")
	request.Header.Set("Access-Control-Request-Private-Network", "true")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("preflight status = %d", response.StatusCode)
	}
	if got := response.Header.Get("Access-Control-Allow-Private-Network"); got != "true" {
		t.Fatalf("private network permission = %q", got)
	}
}

func pairRequest(t *testing.T, url, nonce, origin string) *http.Response {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"nonce": nonce})
	request, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Origin", origin)
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}
