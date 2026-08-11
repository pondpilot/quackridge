package config

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/pondpilot/quackridge/internal/secrets"
)

type testValidator struct{ err error }

func (v testValidator) Validate(context.Context, Source, []byte) error { return v.err }

func TestServiceAddValidatesBeforePersistence(t *testing.T) {
	store := Store{Path: filepath.Join(t.TempDir(), "config.json")}
	credentials := secrets.NewMemory()
	configured := testDocument().Sources[0]
	service := Service{Store: store, Credentials: credentials, Validator: testValidator{err: errors.New("offline")}}
	if err := service.Add(t.Context(), configured, []byte("password")); err == nil {
		t.Fatal("invalid source was added")
	}
	loaded, err := store.Load()
	if err != nil || len(loaded.Sources) != 0 {
		t.Fatalf("configuration mutated: %#v, %v", loaded, err)
	}
	if _, err := credentials.Get(t.Context(), configured.CredentialRef); !errors.Is(err, secrets.ErrNotFound) {
		t.Fatalf("credential persisted: %v", err)
	}
}

func TestServiceAddAndRemoveAreCoordinated(t *testing.T) {
	store := Store{Path: filepath.Join(t.TempDir(), "config.json")}
	credentials := secrets.NewMemory()
	configured := testDocument().Sources[0]
	service := Service{Store: store, Credentials: credentials, Validator: testValidator{}}
	if err := service.Add(t.Context(), configured, []byte("password")); err != nil {
		t.Fatal(err)
	}
	if _, err := credentials.Get(t.Context(), configured.CredentialRef); err != nil {
		t.Fatal(err)
	}
	if err := service.Remove(t.Context(), configured.ID); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load()
	if err != nil || len(loaded.Sources) != 0 {
		t.Fatalf("removed config = %#v, %v", loaded, err)
	}
	if _, err := credentials.Get(t.Context(), configured.CredentialRef); !errors.Is(err, secrets.ErrNotFound) {
		t.Fatalf("removed credential = %v", err)
	}
}
