package quackridge

const (
	Product         = "quackridge"
	Version         = "0.1.0-dev"
	ProtocolVersion = 1
	MetadataVersion = 1
	DuckDBVersion   = "1.5.5"
)

var Capabilities = []string{"cancellation_noop", "metadata_v1", "pairing_v1", "query_ids", "sticky_sessions"}
