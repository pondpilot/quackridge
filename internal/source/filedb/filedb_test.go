package filedb

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/pondpilot/quackridge/internal/engine"
	"github.com/pondpilot/quackridge/internal/source"
)

type fakeAttacher struct{ attachment engine.Attachment }

func (f *fakeAttacher) Attach(_ context.Context, attachment engine.Attachment) error {
	f.attachment = attachment
	return nil
}
func (*fakeAttacher) Detach(context.Context, string, string) error             { return nil }
func (*fakeAttacher) Query(context.Context, string, ...any) (*sql.Rows, error) { return nil, nil }
func (*fakeAttacher) QueryRow(context.Context, string, ...any) *sql.Row        { return &sql.Row{} }

func TestFileAdapterRequiresAbsoluteExistingPathAndAttachesReadOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "source.duckdb")
	if err := os.WriteFile(path, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	definition := source.Definition{ID: "source", Name: "Source", Alias: "source", ConnectorType: "duckdb", DatabaseType: "duckdb", Enabled: true}
	attacher := &fakeAttacher{}
	adapter := New(attacher, "duckdb", Config{Path: path})
	if err := adapter.Attach(t.Context(), definition); err != nil {
		t.Fatal(err)
	}
	if attacher.attachment.Connection != path || !attacher.attachment.ReadOnly || attacher.attachment.Type != "duckdb" {
		t.Fatalf("attachment = %#v", attacher.attachment)
	}
	if err := New(attacher, "sqlite", Config{Path: "relative.db"}).Validate(t.Context(), definition); err == nil {
		t.Fatal("relative database path accepted")
	}
}
