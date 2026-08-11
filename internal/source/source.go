package source

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sync"
)

var aliasPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)

type Definition struct {
	ID      string
	Name    string
	Alias   string
	Type    string
	Enabled bool
}

type MetadataRow struct {
	SourceID        string
	SourceName      string
	SourceType      string
	SourceHealth    string
	CatalogName     string
	SchemaName      *string
	ObjectName      *string
	ObjectType      *string
	ColumnName      *string
	OrdinalPosition *int
	DuckDBType      *string
	Nullable        *bool
	ErrorCode       *string
}

type Adapter interface {
	Type() string
	Validate(context.Context, Definition) error
	Attach(context.Context, Definition) error
	Metadata(context.Context, Definition) ([]MetadataRow, error)
	Health(context.Context, Definition) error
	Cleanup(context.Context, Definition) error
}

type Registry struct {
	mu       sync.RWMutex
	adapters map[string]Adapter
}

func NewRegistry(adapters ...Adapter) (*Registry, error) {
	registry := &Registry{adapters: make(map[string]Adapter, len(adapters))}
	for _, adapter := range adapters {
		if adapter == nil || adapter.Type() == "" {
			return nil, errors.New("source adapter type is required")
		}
		if _, exists := registry.adapters[adapter.Type()]; exists {
			return nil, fmt.Errorf("duplicate source adapter %q", adapter.Type())
		}
		registry.adapters[adapter.Type()] = adapter
	}
	return registry, nil
}

func (r *Registry) Adapter(sourceType string) (Adapter, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	adapter, ok := r.adapters[sourceType]
	return adapter, ok
}

func (r *Registry) Types() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	types := make([]string, 0, len(r.adapters))
	for sourceType := range r.adapters {
		types = append(types, sourceType)
	}
	slices.Sort(types)
	return types
}

func ValidateAlias(alias string) error {
	if !aliasPattern.MatchString(alias) {
		return fmt.Errorf("alias must match %s", aliasPattern)
	}
	return nil
}
