# DuckDB, Quack, and PostgreSQL spike

Date: 2026-08-11

Host: Linux AMD64

Conclusion: data-plane spike passed; cancellation is evaluated separately.

## Version pair and offline artifacts

- DuckDB engine: 1.5.5
- Go driver: `github.com/duckdb/duckdb-go/v2 v2.10505.0`
- Extension repository: `https://extensions.duckdb.org/v1.5.5/<platform>/`
- Explicitly loaded artifacts: `httpfs`, `postgres_scanner`, and `quack`

The spike found that Quack needs `httpfs` for its client transport. With
autoload disabled, bundling only `quack` and `postgres_scanner` makes attach
attempts fail while trying to create the user's DuckDB extension cache. The
offline bundle therefore includes the exact matching signed `httpfs` artifact.
`scripts/fetch-extensions.sh` records and verifies SHA-256 for all v1 release
targets before decompressing an artifact.

Linux AMD64 compressed SHA-256 values:

| Extension | SHA-256 |
| --- | --- |
| `httpfs` | `7cdd52a3135388718884a9b71e3987ba723002121e8e9de399c4ed619d824a05` |
| `postgres_scanner` | `e0f631a5535f165468bc8a20501f8bc1490adbc877d38fcdff2f8d05531e1e5b` |
| `quack` | `7b2c417e3797c2d85673655dea420ead9bbbb24e686ee8dbe37bef9fa8768207` |

## Reproduction and evidence

```sh
./scripts/fetch-extensions.sh /tmp/quackridge-extensions
QUACKRIDGE_EXTENSION_DIR=/tmp/quackridge-extensions \
  go test -tags=integration -run 'TestQuackIdentityAndShutdown|TestPostgresServerSideJoinAndMetadata' -v ./internal/engine
```

The tests prove that one in-memory native engine:

- loads all extensions by absolute local path and then disables autoinstall,
  autoload, and community extensions;
- starts Quack on an allocated loopback port with a generated 256-bit token;
- publishes product, product version, protocol, metadata version, source types,
  read-only posture, and capabilities through `whoami()`;
- starts an isolated PostgreSQL 17 container with a dedicated default-read-only
  role, related tables, UUIDs, decimals, timestamp with time zone, arrays, and
  nullable values;
- attaches that source with `TYPE postgres, READ_ONLY` under a validated alias;
- computes the related-table aggregate `Ada / 19.75` as one server-side query;
- returns the same joined aggregate through a separate native DuckDB Quack
  client and preserves `DECIMAL(18,2)`, `TIMESTAMPTZ`, `VARCHAR[]`, nullable
  `VARCHAR`, and `UUID` result types and values;
- reports eight fixture columns through `quackridge_metadata_v1()`; and
- denies an attempted PostgreSQL insert through the read-only attachment.

`TestUnavailablePostgresDoesNotStopQuackIdentity` reserves and closes a TCP
port, proves the PostgreSQL adapter rejects that unavailable endpoint, then
attaches a separate Quack client and successfully reads `whoami()` and the
empty metadata health relation. Source failure therefore does not prevent the
engine data plane from serving identity and health information.

Stopping the runtime closes DuckDB and the Quack listener; the identity test
then proves that the allocated TCP port cannot be opened. Docker `--rm` cleanup
removes the disposable PostgreSQL container after every test.

## Known limitations

- Quack is beta and its functions and wire contract can change before DuckDB
  2.0.
- `quack_cancel` is absent from the signed 1.5.5 artifact. Cancellation is an
  explicitly accepted experimental no-op documented in
  `security-cancellation-gate.md`.
- Native smoke evidence currently exists only for Linux AMD64. Other release
  targets remain unsupported until their exact archives pass native CI.
