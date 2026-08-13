# Security and cancellation go/no-go gate

Date: 2026-08-11

Pinned pair: DuckDB 1.5.5 / duckdb-go v2.10505.0 / signed Quack 1.5.5

Conclusion: **WAIVED — cancellation is an explicit experimental no-op.**

## Authorization evidence

Authorization passes. `internal/policy` registers a Go scalar UDF named
`quackridge_authorize` in DuckDB's global catalog and configures Quack to call it
before every prepared request. An integration test proves that the registered
function is visible from a second DuckDB connection; the real Quack attach and
identity tests prove it is also visible from Quack's fresh transient callback
connections.

The UDF uses DuckDB's C parser to require exactly one complete statement.
Analytical `SELECT` trees are produced with `json_serialize_sql` and recursively
inspected. Unknown table functions, filesystem-looking replacement scans,
network readers, PostgreSQL pass-through, and dynamic-query functions fail
closed even when nested in CTEs or subqueries. Transaction statements are
classified through `duckdb_prepared_statement_type`. Temporary objects are
limited to unquoted `qr_tmp_*` tables whose complete `AS SELECT` tree passes the
same inspection. Persistent DDL, DML, attach/detach, extension operations,
secrets, configuration, prepared statements, and multiple statements are
denied.

Reproduce the adversarial suite:

```sh
go test -run TestAdversarialPolicy -v ./internal/policy
QUACKRIDGE_EXTENSION_DIR=/tmp/quackridge-extensions \
  go test -tags=integration -run TestQuackIdentityAndShutdown -v ./internal/engine
```

## Cancellation evidence

Context cancellation on the native DuckDB client did not stop this server query
within the three-second threshold:

```sql
/* quackridge-query-id:test-cancel */
SELECT sum(i)::UBIGINT AS total FROM range(1000000000000) t(i)
```

Quack's source branch contains an explicit `quack_cancel(catalog,
connection_id)` table function and a `CANCEL_REQUEST` implementation. The
server-side `quack_active_connections()` relation in the signed 1.5.5 artifact
does expose the active connection and the query-ID comment, and QuackRidge can
expose a sanitized `quackridge_active_queries_v1()` relation. However, the
client call fails against the exact signed artifact with:

```text
Catalog Error: Table Function with name quack_cancel does not exist!
Did you mean "quack_clear_cache"?
```

Binary string inspection also finds `quack_active_connections` and
`quack_clear_cache` but no `quack_cancel`. The function exists on the Quack
`main` source line, but that line targets a newer DuckDB development commit and
is not a signed, version-compatible 1.5.5 release artifact.

Because cancellation cannot be issued, abandoned-stream reclamation and bounded
server resource cleanup cannot be established for the pinned release pair.

## Accepted experimental deviation

On 2026-08-11 the project owner explicitly directed implementation to continue
with cancellation as a no-op. QuackRidge therefore advertises
`cancellation_noop`, never `cancel`. PondPilot must disable or label cancellation
for this target rather than implying that native work stopped.

This waiver accepts that closing, abandoning, or cancelling a browser result can
leave native DuckDB/PostgreSQL work running until it finishes naturally or hits
a separately enforced resource limit. It does not reinterpret the failed test as
a successful cancellation.

The deviation should be removed by one of these follow-up changes:

1. Pin a future signed DuckDB/Quack release that includes `quack_cancel`, then
   rerun authorization, cancellation, streaming-abandonment, and compatibility
   tests on every supported native platform.
2. Revise the data transport to a QuackRidge-owned protocol with an explicit
   query-ID cancellation operation and bounded server cleanup.
3. Add a process-per-query sandbox whose supervisor can terminate abandoned
   work without corrupting healthy sessions, accepting the loss or redesign of
   sticky session semantics.

The authorization and sandbox work may continue, and experimental releases may
proceed only while the no-op capability and operational risk remain visible.

## Sandbox evidence after the waiver

The integration gate now starts DuckDB with 64 MB memory, 16 MB temporary
storage, and one thread; verifies the settings and configuration lock; and
forces a real memory-limit failure that maps to `QR_RESOURCE_EXHAUSTED`. It also
proves local file reads, extension installation, settings changes, persistent
secret creation, and writes are denied.

`postgres_scanner` requires a temporary secret to attach while persistent
secrets are disabled and local paths are allowlisted. QuackRidge creates a
named in-memory PostgreSQL secret from structured fields, attaches through that
secret, disables PostgreSQL-backed secret-table discovery, and never places the
credential-bearing DSN in an error. The disposable PostgreSQL join and metadata
test passes under these locked settings.

A local context deadline maps to the sanitized `QR_TIMEOUT` code. Through Quack,
however, that deadline is observer-only because the pinned extension has no
native cancellation operation; it carries the same risk accepted above.
