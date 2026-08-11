# Security and cancellation go/no-go gate

Date: 2026-08-11

Pinned pair: DuckDB 1.5.5 / duckdb-go v2.10505.0 / signed Quack 1.5.5

Conclusion: **FAIL — downstream implementation is blocked.**

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

## Decision required to continue

The approved plan requires a hard stop on FAIL. Do not start the reusable
production service, PondPilot UI, packaging, or release tasks until one of these
changes is separately reviewed and approved:

1. Pin a future signed DuckDB/Quack release that includes `quack_cancel`, then
   rerun authorization, cancellation, streaming-abandonment, and compatibility
   tests on every supported native platform.
2. Revise the data transport to a QuackRidge-owned protocol with an explicit
   query-ID cancellation operation and bounded server cleanup.
3. Add a process-per-query sandbox whose supervisor can terminate abandoned
   work without corrupting healthy sessions, accepting the loss or redesign of
   sticky session semantics.

Until one of these paths passes, the gate remains **FAIL** and no experimental
release may be published.
