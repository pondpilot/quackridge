package managementv2

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

type envelope struct {
	Version   int             `json:"version"`
	RequestID string          `json:"request_id"`
	Operation string          `json:"operation"`
	OK        *bool           `json:"ok"`
	Payload   json.RawMessage `json:"payload"`
	Result    json.RawMessage `json:"result"`
}

func TestManagementFixtures(t *testing.T) {
	valid, err := filepath.Glob("fixtures/*.valid.json")
	if err != nil || len(valid) < 3 {
		t.Fatalf("valid fixtures: %v, %d", err, len(valid))
	}
	for _, path := range valid {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var value envelope
		if err := json.Unmarshal(data, &value); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if value.Version != 2 || value.RequestID == "" {
			t.Fatalf("%s: invalid envelope", path)
		}
		if value.Operation == "" && value.OK == nil {
			t.Fatalf("%s: request or response discriminator required", path)
		}
	}
}
