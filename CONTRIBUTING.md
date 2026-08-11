# Contributing

QuackRidge is experimental. Discuss protocol, security-boundary, and adapter
changes before implementation. Keep the public facade small; database behavior
belongs under `internal/` and the CLI only composes public operations.

Run `make check` before opening a pull request. Integration tests must provision
disposable services and must not read a developer credential store or database.
Never include tokens, passwords, DSNs, query text, or private result data in
commits, fixtures, logs, screenshots, or bug reports.
