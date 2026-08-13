# Compatibility contract

QuackRidge uses one tested data-plane combination. Startup fails with
`QR_PROTOCOL_MISMATCH` if the embedded engine or any loaded extension reports a
different build identifier.

| Component | Supported version/build |
| --- | --- |
| DuckDB | 1.5.5 |
| duckdb-go | v2.10505.0 |
| httpfs | `827222f` |
| mysql_scanner | `7267164` |
| odbc_scanner | `274a330` |
| postgres_scanner | `41223e5` |
| Quack | `c154811` |
| sqlite_scanner | `f79b1db` |

QuackRidge protocol v2 requires product `quackridge`, metadata version 2,
the complete connector set, read-only mode, and the complete v2 capability set.
Clients must reject generic Quack servers, missing identity fields, older or
newer protocol versions, and missing or unknown capabilities. The normative
JSON schemas and examples are under [`protocol/v2`](../protocol/v2/).

The current Quack build has sticky server sessions and carries PondPilot query
IDs in a leading `/* quackridge-query-id:<id> */` comment. That comment does not
change SQL semantics and is emitted only as a sanitized structured-log field.
Cancellation is the documented `cancellation_noop` capability for this exact
pair; clients must not present it as server-side cancellation.
