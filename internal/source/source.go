package source

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sync"
)

var aliasPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)

type Definition struct {
	ID            string
	Name          string
	Alias         string
	ConnectorType string
	DatabaseType  string
	Enabled       bool
}

type MetadataRow struct {
	SourceID        string
	SourceName      string
	ConnectorType   string
	DatabaseType    string
	SourceHealth    string
	CatalogName     string
	SchemaName      *string
	ObjectName      *string
	ObjectType      *string
	ColumnName      *string
	OrdinalPosition *int
	DuckDBType      *string
	Nullable        *bool
	IsSystemSchema  *bool
	ErrorCode       *string
}

type MetadataQueryer interface {
	Query(context.Context, string, ...any) (*sql.Rows, error)
}

func ReadMetadata(ctx context.Context, queryer MetadataQueryer, sourceID string) ([]MetadataRow, error) {
	rows, err := queryer.Query(ctx, `SELECT source_id, source_name, connector_type, database_type, source_health,
		catalog_name, schema_name, object_name, object_type, column_name, ordinal_position,
		duckdb_type, nullable, is_system_schema, error_code FROM quackridge_metadata_v2() WHERE source_id = ?
		ORDER BY schema_name, object_name, ordinal_position`, sourceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var metadata []MetadataRow
	for rows.Next() {
		var row MetadataRow
		var schemaName, objectName, objectType, columnName, duckDBType, errorCode sql.NullString
		var ordinal sql.NullInt64
		var nullable, isSystemSchema sql.NullBool
		if err := rows.Scan(&row.SourceID, &row.SourceName, &row.ConnectorType, &row.DatabaseType, &row.SourceHealth,
			&row.CatalogName, &schemaName, &objectName, &objectType, &columnName, &ordinal,
			&duckDBType, &nullable, &isSystemSchema, &errorCode); err != nil {
			return nil, err
		}
		row.SchemaName = nullStringPointer(schemaName)
		row.ObjectName = nullStringPointer(objectName)
		row.ObjectType = nullStringPointer(objectType)
		row.ColumnName = nullStringPointer(columnName)
		row.DuckDBType = nullStringPointer(duckDBType)
		row.ErrorCode = nullStringPointer(errorCode)
		if ordinal.Valid {
			value := int(ordinal.Int64)
			row.OrdinalPosition = &value
		}
		if nullable.Valid {
			value := nullable.Bool
			row.Nullable = &value
		}
		if isSystemSchema.Valid {
			value := isSystemSchema.Bool
			row.IsSystemSchema = &value
		}
		metadata = append(metadata, row)
	}
	return metadata, rows.Err()
}

func nullStringPointer(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	return &value.String
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
