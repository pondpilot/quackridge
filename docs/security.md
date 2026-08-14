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

After startup, QuackRidge also rejects community and unsigned extensions,
disables persistent DuckDB secrets, restricts local access to the engine sandbox,
the verified extension directory, and explicitly configured database files,
constrains memory, threads, and temporary storage, and locks DuckDB configuration. PostgreSQL
credentials are transferred into a temporary in-memory DuckDB secret instead of
being embedded in an `ATTACH` error or persisted to disk. The engine sandbox is
removed on shutdown.

PostgreSQL credentials are the final authorization boundary. Use a dedicated
role without superuser, role creation, database creation, replication, or bypass
RLS; grant only `CONNECT`, schema `USAGE`, and table `SELECT`. QuackRidge also
uses DuckDB's `READ_ONLY` attachment option.

Logs contain component, query identifier, source identifier, duration, and error
code. They omit SQL text, DSNs, tokens, passwords, private paths, and result data.

## Cancellation and timeouts

The pinned signed Quack 1.5.5 extension does not expose `quack_cancel`.
QuackRidge therefore advertises `cancellation_noop`, not `cancel`. A client-side
deadline can report `QR_TIMEOUT`, but it does not prove that native server work
stopped. Closing or abandoning a result can leave work running until it finishes
or reaches a memory or temporary-storage limit. See the
[cancellation gate](spikes/security-cancellation-gate.md) for evidence and the
accepted experimental deviation.

## Non-goals and trusted boundary

Version 0.1 does not provide write access, cross-engine joins, remote Quack
exposure, multi-user sharing, a privileged background service, or automatic
updates. The native macOS menu-bar app supervises an unprivileged per-user
backend and may register itself as an opt-in Login Item. The
local operating-system user and the QuackRidge executable/extension directory
are trusted. Browser content, pairing input, tokens, SQL, PostgreSQL responses,
and network failures are untrusted. QuackRidge is a local isolation layer, not
a multi-tenant security boundary.

The app verifies a generated manifest of bundled backend bytes before launch.
Its management socket is user-only and bounded; raw backend stdout/stderr is
discarded and cannot be interpreted as a trusted lifecycle or log event. The
app is not sandboxed because the backend must open configured database files
and external ODBC drivers. Hardened Runtime and distribution signing are release
gates applied after unsigned implementation validation.
