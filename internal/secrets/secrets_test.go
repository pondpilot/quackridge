package secrets

import (
	"context"
	"errors"
	"testing"
)

func TestMemoryClonesValuesAndDeletes(t *testing.T) {
	store := NewMemory()
	value := []byte("temporary-password")
	if err := store.Put(t.Context(), "source/warehouse", value); err != nil {
		t.Fatal(err)
	}
	value[0] = 'X'
	got, err := store.Get(t.Context(), "source/warehouse")
	if err != nil || string(got) != "temporary-password" {
		t.Fatalf("get = %q, %v", got, err)
	}
	got[0] = 'X'
	again, _ := store.Get(t.Context(), "source/warehouse")
	if string(again) != "temporary-password" {
		t.Fatal("caller mutated stored credential")
	}
	if err := store.Delete(t.Context(), "source/warehouse"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(t.Context(), "source/warehouse"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted get error = %v", err)
	}
}

func TestEnvironmentIsExplicitAndReadOnly(t *testing.T) {
	store := Environment{}
	t.Setenv("QUACKRIDGE_SECRET_SOURCE_WAREHOUSE", "environment-password")
	got, err := store.Get(context.Background(), "source/warehouse")
	if err != nil || string(got) != "environment-password" {
		t.Fatalf("get = %q, %v", got, err)
	}
	if err := store.Put(t.Context(), "source/warehouse", []byte("new")); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("put error = %v", err)
	}
}
