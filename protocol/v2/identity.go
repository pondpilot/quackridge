// Package v2 defines the machine-readable QuackRidge protocol v2 contract.
package v2

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"slices"

	quackridge "github.com/pondpilot/quackridge"
)

var productVersionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:[-+][0-9A-Za-z.-]+)?$`)

type Identity struct {
	Product         string   `json:"product"`
	ProductVersion  string   `json:"product_version"`
	ProtocolVersion int      `json:"protocol_version"`
	MetadataVersion int      `json:"metadata_version"`
	ConnectorTypes  []string `json:"connector_types"`
	ReadOnly        bool     `json:"read_only"`
	Capabilities    []string `json:"capabilities"`
}

func CurrentIdentity() Identity {
	return Identity{
		Product: quackridge.Product, ProductVersion: quackridge.Version,
		ProtocolVersion: quackridge.ProtocolVersion, MetadataVersion: quackridge.MetadataVersion,
		ConnectorTypes: []string{"duckdb", "mysql", "odbc", "postgres", "sqlite"}, ReadOnly: true, Capabilities: quackridge.Capabilities(),
	}
}

func DecodeIdentity(data []byte) (Identity, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var identity Identity
	if err := decoder.Decode(&identity); err != nil {
		return Identity{}, mismatch("identity is malformed", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Identity{}, mismatch("identity is malformed", err)
	}
	if err := ValidateIdentity(identity); err != nil {
		return Identity{}, err
	}
	return identity, nil
}

func ValidateIdentity(identity Identity) error {
	switch {
	case identity.Product != quackridge.Product:
		return mismatch("server is not QuackRidge", nil)
	case !productVersionPattern.MatchString(identity.ProductVersion):
		return mismatch("product version is invalid", nil)
	case identity.ProtocolVersion != quackridge.ProtocolVersion:
		return mismatch("protocol version is unsupported", nil)
	case identity.MetadataVersion != quackridge.MetadataVersion:
		return mismatch("metadata version is unsupported", nil)
	case !identity.ReadOnly:
		return mismatch("read-only identity is required", nil)
	case !slices.Equal(identity.ConnectorTypes, []string{"duckdb", "mysql", "odbc", "postgres", "sqlite"}):
		return mismatch("connector capability set is unsupported", nil)
	}
	requiredCapabilities := quackridge.Capabilities()
	if len(identity.Capabilities) != len(requiredCapabilities) {
		return mismatch("capability set is unsupported", nil)
	}
	for _, required := range requiredCapabilities {
		if !slices.Contains(identity.Capabilities, required) {
			return mismatch("required capability is missing", nil)
		}
	}
	return nil
}

func mismatch(message string, cause error) error {
	return &quackridge.Error{Code: quackridge.CodeProtocolMismatch, Message: message, Cause: cause}
}

type Column struct {
	Name       string
	DuckDBType string
}

var MetadataColumns = []Column{
	{Name: "source_id", DuckDBType: "VARCHAR"},
	{Name: "source_name", DuckDBType: "VARCHAR"},
	{Name: "connector_type", DuckDBType: "VARCHAR"},
	{Name: "database_type", DuckDBType: "VARCHAR"},
	{Name: "source_health", DuckDBType: "VARCHAR"},
	{Name: "catalog_name", DuckDBType: "VARCHAR"},
	{Name: "schema_name", DuckDBType: "VARCHAR"},
	{Name: "object_name", DuckDBType: "VARCHAR"},
	{Name: "object_type", DuckDBType: "VARCHAR"},
	{Name: "column_name", DuckDBType: "VARCHAR"},
	{Name: "ordinal_position", DuckDBType: "INTEGER"},
	{Name: "duckdb_type", DuckDBType: "VARCHAR"},
	{Name: "nullable", DuckDBType: "BOOLEAN"},
	{Name: "is_system_schema", DuckDBType: "BOOLEAN"},
	{Name: "error_code", DuckDBType: "VARCHAR"},
}
