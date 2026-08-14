// Package connectors is the single registry for source validation and adapter construction.
package connectors

import (
	"context"
	"errors"

	"github.com/pondpilot/quackridge/internal/config"
	"github.com/pondpilot/quackridge/internal/reconcile"
	"github.com/pondpilot/quackridge/internal/source"
)

type Registry struct {
	factories map[string]reconcile.Factory
}

func New() *Registry { return NewWithCertificates(nil) }

func NewWithCertificates(certificates reconcile.RootCertificateResolver) *Registry {
	values := []reconcile.Factory{
		reconcile.PostgresFactory{Certificates: certificates},
		reconcile.MySQLFactory{},
		reconcile.FileFactory{Connector: "sqlite"},
		reconcile.FileFactory{Connector: "duckdb"},
		reconcile.ODBCFactory{},
	}
	registry := &Registry{factories: make(map[string]reconcile.Factory, len(values))}
	for _, factory := range values {
		registry.factories[factory.Type()] = factory
	}
	return registry
}

func (r *Registry) Types() []string {
	return []string{"postgres", "mysql", "sqlite", "duckdb", "odbc"}
}

func (r *Registry) Validate(ctx context.Context, configured config.Source, credential []byte) error {
	if r == nil {
		return errors.New("connector registry is required")
	}
	factory, ok := r.factories[configured.Type]
	if !ok {
		return errors.New("source adapter is unavailable")
	}
	adapter, err := factory.Build(configured, credential)
	if err != nil {
		return err
	}
	return adapter.Validate(ctx, source.Definition{
		ID: configured.ID, Name: configured.Name, Alias: configured.Alias,
		ConnectorType: configured.Type, DatabaseType: configured.DatabaseType, Enabled: configured.Enabled,
	})
}
