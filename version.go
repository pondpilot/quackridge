package quackridge

import "slices"

const (
	Product         = "quackridge"
	Version         = "0.1.0-dev"
	ProtocolVersion = 1
	MetadataVersion = 1
	DuckDBVersion   = "1.5.5"
)

var capabilities = []string{"cancellation_noop", "metadata_v1", "pairing_v1", "query_ids", "sticky_sessions"}
var extensionVersions = map[string]string{
	"httpfs": "827222f", "postgres_scanner": "41223e5", "quack": "c154811",
}

// Capabilities returns a copy of the immutable protocol capability set.
func Capabilities() []string { return slices.Clone(capabilities) }

// ExtensionVersions returns the exact signed extension build identifiers in
// the supported DuckDB/Quack version pair.
func ExtensionVersions() map[string]string {
	versions := make(map[string]string, len(extensionVersions))
	for name, version := range extensionVersions {
		versions[name] = version
	}
	return versions
}
