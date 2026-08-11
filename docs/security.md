# Security model

QuackRidge accepts SQL only from a locally paired PondPilot browser, but treats
the browser, token, origin, and submitted SQL as potentially compromised. The
Quack listener is loopback-only. Pairing is short-lived, origin-bound,
single-use, and returns no source credentials.

The parser-backed policy allows analytical reads, metadata, transaction control,
and explicitly scoped temporary objects. It denies persistent DDL, DML,
`ATTACH`/`DETACH`, `INSTALL`/`LOAD`, secrets, settings, filesystem access,
network table functions, extension functions, and PostgreSQL pass-through.
Required extensions are loaded from verified local files before autoload and
autoinstall are disabled.

PostgreSQL credentials are the final authorization boundary. Use a dedicated
role without superuser, role creation, database creation, replication, or bypass
RLS; grant only `CONNECT`, schema `USAGE`, and table `SELECT`. QuackRidge also
uses DuckDB's `READ_ONLY` attachment option.

Logs contain component, query identifier, source identifier, duration, and error
code. They omit SQL text, DSNs, tokens, passwords, private paths, and result data.
