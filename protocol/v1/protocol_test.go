package v1_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
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
