# Compatibility contract

QuackRidge v0.1 uses one tested data-plane combination. Startup fails with
`QR_PROTOCOL_MISMATCH` if the embedded engine or any loaded extension reports a
different build identifier.

| Component | Supported version/build |
| --- | --- |
| DuckDB | 1.5.5 |
| duckdb-go | v2.10505.0 |
| httpfs | `827222f` |
| postgres_scanner | `41223e5` |
| Quack | `c154811` |

QuackRidge protocol v1 requires product `quackridge`, metadata version 1,
PostgreSQL source support, read-only mode, and the complete v1 capability set.
Clients must reject generic Quack servers, missing identity fields, older or
newer protocol versions, and missing or unknown capabilities. The normative
JSON schemas and examples are under [`protocol/v1`](../protocol/v1/).

The current Quack build has sticky server sessions and carries PondPilot query
IDs in a leading `/* quackridge-query-id:<id> */` comment. That comment does not
change SQL semantics and is emitted only as a sanitized structured-log field.
Cancellation is the documented `cancellation_noop` capability for this exact
pair; clients must not present it as server-side cancellation.
