# QuackRidge

> [!WARNING]
> QuackRidge is experimental software. Quack is beta, protocol compatibility can
> change, and no production stability guarantee is made before the v1 gates pass.

QuackRidge is PondPilot's loopback-only, read-only PostgreSQL bridge. It embeds
DuckDB, attaches private PostgreSQL sources locally, and exposes complete
server-side statements to PondPilot through the Quack protocol. Source
credentials never enter the browser.

The project is licensed under Apache-2.0. The supported v1 release targets are
macOS AMD64, macOS ARM64, Linux AMD64, and Windows AMD64. An artifact is not
supported until its native smoke test has passed.

## Development

QuackRidge requires Go 1.26 or newer, CGO, and platform build tools.

```sh
make check
go run ./cmd/quackridge version
```

Integration and end-to-end tests are opt-in and never use an existing database
or credential store:

```sh
make integration
make e2e
```

See [the implementation plan](docs/plans/2026-08-11-quackridge-v1.md),
[security policy](docs/security.md), and [contribution guide](CONTRIBUTING.md).
