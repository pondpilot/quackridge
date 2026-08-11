package postgres

import (
	"strings"
	"testing"
)

func TestConnectionStringIsBuiltOnlyInMemory(t *testing.T) {
	adapter := New(nil, Config{Host: "localhost", Port: 5432, Database: "db", User: "reader", SSLMode: "require"}, Credential{Password: "s'ecret"})
	value := adapter.connectionString()
	for _, part := range []string{"host='localhost'", "port='5432'", "password='s\\'ecret'", "sslmode='require'"} {
		if !strings.Contains(value, part) {
			t.Errorf("connection string missing expected non-literal part %q", part)
		}
	}
}

func TestSSLMode(t *testing.T) {
	for _, mode := range []string{"disable", "allow", "prefer", "require", "verify-ca", "verify-full"} {
		if !validSSLMode(mode) {
			t.Errorf("valid mode rejected: %s", mode)
		}
	}
	if validSSLMode("trust-me") {
		t.Fatal("unsafe mode accepted")
	}
}
