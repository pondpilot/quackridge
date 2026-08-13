package v2_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	quackridge "github.com/pondpilot/quackridge"
	protocol "github.com/pondpilot/quackridge/protocol/v2"
)

func TestFixturesAreMachineReadableJSON(t *testing.T) {
	files, err := filepath.Glob("fixtures/*.json")
	if err != nil || len(files) == 0 {
		t.Fatalf("fixtures: %v (%d files)", err, len(files))
	}
	for _, name := range files {
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		var value any
		if err := json.Unmarshal(data, &value); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestIdentityCompatibilityFailsClosed(t *testing.T) {
	data, err := os.ReadFile("fixtures/identity.valid.json")
	if err != nil {
		t.Fatal(err)
	}
	current, err := protocol.DecodeIdentity(data)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		mutate func(*protocol.Identity)
	}{
		{"older", func(value *protocol.Identity) { value.ProtocolVersion-- }},
		{"newer", func(value *protocol.Identity) { value.ProtocolVersion++ }},
		{"missing protocol", func(value *protocol.Identity) { value.ProtocolVersion = 0 }},
		{"missing capability", func(value *protocol.Identity) { value.Capabilities = value.Capabilities[1:] }},
		{"unknown capability", func(value *protocol.Identity) { value.Capabilities[0] = "unknown" }},
		{"generic Quack", func(value *protocol.Identity) { value.Product = "quack" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := current
			candidate.ConnectorTypes = slices.Clone(current.ConnectorTypes)
			candidate.Capabilities = slices.Clone(current.Capabilities)
			test.mutate(&candidate)
			if err := protocol.ValidateIdentity(candidate); !quackridge.IsCode(err, quackridge.CodeProtocolMismatch) {
				t.Fatalf("ValidateIdentity() = %v", err)
			}
		})
	}
}

func TestPairingFixtureContainsOnlyConnectionContract(t *testing.T) {
	data, err := os.ReadFile("fixtures/pairing.valid.json")
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]json.RawMessage
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatal(err)
	}
	if len(value) != 3 || value["endpoint"] == nil || value["identity"] == nil || value["token"] == nil {
		t.Fatalf("pairing fields = %v", value)
	}
}
