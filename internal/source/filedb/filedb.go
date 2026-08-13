package filedb

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	"github.com/pondpilot/quackridge/internal/engine"
	"github.com/pondpilot/quackridge/internal/source"
)

type Config struct {
	Path string `json:"path"`
}

type Attacher interface {
	Attach(context.Context, engine.Attachment) error
	Detach(context.Context, string, string) error
	Query(context.Context, string, ...any) (*sql.Rows, error)
	QueryRow(context.Context, string, ...any) *sql.Row
}

type Adapter struct {
	engine    Attacher
	connector string
	config    Config
}

func New(attacher Attacher, connector string, config Config) *Adapter {
	return &Adapter{engine: attacher, connector: connector, config: config}
}

func (a *Adapter) Type() string { return a.connector }

func (a *Adapter) Validate(_ context.Context, definition source.Definition) error {
	if definition.ID == "" || definition.Name == "" {
		return fmt.Errorf("source id and name are required")
	}
	if err := source.ValidateAlias(definition.Alias); err != nil {
		return err
	}
	if a.connector != "sqlite" && a.connector != "duckdb" {
		return fmt.Errorf("unsupported file connector")
	}
	if definition.DatabaseType != "" && definition.DatabaseType != a.connector {
		return fmt.Errorf("file database type must match its connector")
	}
	if !filepath.IsAbs(a.config.Path) {
		return fmt.Errorf("database path must be absolute")
	}
	info, err := os.Stat(a.config.Path)
	if err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("database file is unavailable")
	}
	return nil
}

func (a *Adapter) Attach(ctx context.Context, definition source.Definition) error {
	if err := a.Validate(ctx, definition); err != nil {
		return err
	}
	return a.engine.Attach(ctx, engine.Attachment{
		SourceID: definition.ID, SourceName: definition.Name, Alias: definition.Alias,
		Type: a.connector, DatabaseType: a.connector, Connection: a.config.Path, ReadOnly: true,
	})
}

func (a *Adapter) Metadata(ctx context.Context, definition source.Definition) ([]source.MetadataRow, error) {
	return source.ReadMetadata(ctx, a.engine, definition.ID)
}

func (a *Adapter) Health(ctx context.Context, definition source.Definition) error {
	var count int
	if err := a.engine.QueryRow(ctx, "SELECT count(*) FROM duckdb_databases() WHERE database_name = ?", definition.Alias).Scan(&count); err != nil || count != 1 {
		return fmt.Errorf("database file health check failed")
	}
	return nil
}

func (a *Adapter) Cleanup(ctx context.Context, definition source.Definition) error {
	return a.engine.Detach(ctx, definition.Alias, definition.ID)
}

var _ source.Adapter = (*Adapter)(nil)
