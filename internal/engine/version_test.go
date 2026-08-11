package engine

import (
	"testing"

	quackridge "github.com/pondpilot/quackridge"
)

func TestValidateVersionPair(t *testing.T) {
	valid := quackridge.ExtensionVersions()
	if err := validateVersionPair("v1.5.5", valid); err != nil {
		t.Fatal(err)
	}
	for name, test := range map[string]struct {
		engine     string
		extensions map[string]string
	}{
		"engine drift":  {engine: "v1.6.0", extensions: valid},
		"missing Quack": {engine: "v1.5.5", extensions: map[string]string{"httpfs": "827222f", "postgres_scanner": "41223e5"}},
		"Quack drift":   {engine: "v1.5.5", extensions: map[string]string{"httpfs": "827222f", "postgres_scanner": "41223e5", "quack": "different"}},
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateVersionPair(test.engine, test.extensions); !quackridge.IsCode(err, quackridge.CodeProtocolMismatch) {
				t.Fatalf("validateVersionPair() = %v", err)
			}
		})
	}
}
