package quackridge

import "slices"

const (
	Product         = "quackridge"
	ProtocolVersion = 2
	MetadataVersion = 2
	DuckDBVersion   = "1.5.5"
)

// Version is replaced by release builds with -ldflags -X. Development builds
// deliberately identify themselves as unshipped.
var Version = "0.1.0-dev"

var capabilities = []string{"cancellation_noop", "metadata_v2", "pairing_v2", "query_ids", "sticky_sessions"}
var extensionVersions = map[string]string{
	"httpfs": "827222f", "mysql_scanner": "7267164", "odbc_scanner": "274a330",
	"postgres_scanner": "41223e5", "quack": "c154811", "sqlite_scanner": "f79b1db",
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
