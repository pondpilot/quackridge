package postgres

import (
	"testing"
)

func TestCredentialIsSeparatedIntoTemporarySecretValues(t *testing.T) {
	adapter := New(nil, Config{Host: "localhost", Port: 5432, Database: "db", User: "reader", SSLMode: "require"}, Credential{Password: "s'ecret"})
	values := adapter.secretValues()
	for key, want := range map[string]string{"HOST": "localhost", "PORT": "5432", "PASSWORD": "s'ecret", "SSLMODE": "require"} {
		if values[key] != want {
			t.Errorf("%s = %q, want %q", key, values[key], want)
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
