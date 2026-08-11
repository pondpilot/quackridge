# Plan: QuackRidge v1 — Local PostgreSQL Bridge for PondPilot

Build QuackRidge as a standalone Apache-2.0 Go project at `pondpilot/quackridge`, then integrate it with PondPilot as an explicit local execution target. The implementation follows a vertical-spike-first sequence: prove the risky DuckDB/Quack, authorization, cancellation, and packaging seams before building production abstractions or browser UI. Version one is read-only, PostgreSQL-only, loopback-only, and experimental while Quack remains beta.

The approved design is in `../pondpilot/docs/designs/2026-08-11-quackridge.md`. Supported v1 release targets are macOS AMD64, macOS ARM64, Linux AMD64, and Windows AMD64. Linux ARM64 and Windows ARM64 remain follow-up targets until native CI evidence is available.

## Validation Commands

Run from the QuackRidge repository unless a different directory is specified:

- `test -z "$(gofmt -l .)"`
- `go vet ./...`
- `go test ./...`
- `go test -race ./...`
- `govulncheck ./...`
- `go test -tags=integration ./...`
- `go test -tags=e2e ./...`
- `go build ./cmd/quackridge`
- `./scripts/verify-release-assets.sh dist/`
- `(cd ../pondpilot && yarn typecheck)`
- `(cd ../pondpilot && yarn lint)`
- `(cd ../pondpilot && yarn prettier)`
- `(cd ../pondpilot && yarn test:unit)`
- `(cd ../pondpilot && yarn build)`
- `(cd ../pondpilot && RUN_QUACKRIDGE_E2E=true QUACKRIDGE_BINARY=../quackridge/bin/quackridge yarn playwright test tests/integration/datasource-wizard/quackridge.spec.ts)`

### Task 1: Bootstrap the standalone repository and protocol contract

Create the independent QuackRidge project, establish its public package boundary, and make the protocol contract testable before database code is introduced. This task also creates the remote repository and baseline CI needed by every later task.

- [x] Create the public `pondpilot/quackridge` GitHub repository with `main` as its default branch, Apache-2.0 licensing, an accurate description, and relevant DuckDB/PostgreSQL/PondPilot topics.
- [x] Initialize the local repository with Go module path `github.com/pondpilot/quackridge`; preserve this plan under `docs-plans/`.
- [x] Add the Apache-2.0 `LICENSE`, `README.md`, `CONTRIBUTING.md`, `SECURITY.md`, `.gitignore`, `.editorconfig`, and an experimental-status notice.
- [x] Create the root `quackridge` facade package, the `cmd/quackridge` command skeleton, and empty `internal/engine`, `internal/source`, `internal/policy`, `internal/config`, `internal/secrets`, `internal/control`, and `internal/pairing` boundaries.
- [x] Add `protocol/v1` machine-readable definitions for identity, capabilities, metadata rows, pairing responses, stable error codes, and release manifests.
- [x] Add valid and invalid protocol fixtures that can be consumed by both Go and TypeScript tests without code generation being required for basic validation.
- [x] Add Makefile or task-script entry points for formatting, vetting, tests, race tests, vulnerability checks, integration tests, and builds.
- [x] Add pull-request CI for formatting, `go vet`, unit tests, race tests, and `govulncheck`; pin action versions and the supported Go toolchain.
- [x] Document the v1 platform matrix and the rule that an artifact is unsupported until its native smoke test passes.
- [x] Verify the repository builds and tests cleanly without DuckDB or platform credential-store dependencies.

### Task 2: Prove embedded DuckDB, packaged extensions, and the Quack data plane

Build the smallest runnable vertical slice in Go. The result must start native DuckDB, load locally packaged extensions, attach a disposable PostgreSQL database, serve it through Quack, and execute a multi-table query from another DuckDB client.

- [x] Pin a DuckDB Go client and DuckDB engine version that is binary-compatible with pinned signed `postgres` and `quack` extension artifacts.
- [x] Add a development-only artifact fetch script that downloads the exact platform extensions, records their upstream URLs and SHA-256 hashes, and refuses checksum drift.
- [x] Start one in-memory native DuckDB engine from Go and load the packaged extensions by explicit path before extension autoloading is disabled.
- [x] Start Quack on an ephemeral loopback port with a generated token and publish QuackRidge protocol/version capabilities through `quack_identify`.
- [x] Launch an ephemeral PostgreSQL container with representative schemas, related tables, decimals, timestamps, arrays, nullable columns, and a dedicated read-only role.
- [x] Attach PostgreSQL under a validated source alias with `TYPE postgres, READ_ONLY` and execute a joined aggregate as one server-side query.
- [x] Connect from a separate native DuckDB Quack client, invoke the attached catalog's `query(...)` macro, and verify the joined result and complex types.
- [x] Expose a prototype QuackRidge-owned metadata relation that reports the attached PostgreSQL schemas, objects, columns, ordinals, types, nullability, and health.
- [x] Prove failed PostgreSQL startup does not prevent Quack from serving engine identity and health information.
- [x] Stop Quack and DuckDB through Go context cancellation and verify the port, database handles, and PostgreSQL container are released.
- [x] Record exact version/platform findings and unresolved limitations in `docs/spikes/duckdb-quack.md`.

### Task 3: Pass the authorization, cancellation, and sandbox go/no-go gate

Prove the security assumptions before production library or UI work begins. If parser-backed authorization cannot run before Quack executes SQL, or if abandoned work cannot be stopped within a bounded interval, stop implementation and document the transport/sandbox decision required to continue.

- [x] Determine and implement the safest supported way for Quack's authorization callback to call a parser-backed policy, preferring a registered Go/DuckDB function over a regular-expression SQL macro.
- [x] Parse the complete statement tree and allow analytical reads, metadata statements, transaction control, and explicitly scoped temporary objects only.
- [x] Reject persistent DDL, DML, `ATTACH`/`DETACH`, `INSTALL`/`LOAD`, secrets, configuration changes, filesystem access, network table functions, and dangerous operations nested inside `SELECT` or macros.
- [x] Add adversarial policy fixtures covering comments, Unicode whitespace, CTEs, nested functions, prepared statements, multiple statements, quoting tricks, and PostgreSQL pass-through functions.
- [x] Disable autoinstall, autoload, community and unsigned extensions, persistent DuckDB secrets, and the local filesystem after required startup; use temporary in-memory PostgreSQL secrets, set resource limits, and lock configuration.
- [x] Verify local file reads, arbitrary extension installs, configuration changes, persistent secrets, and write attempts fail after the engine is locked.
- [x] **WAIVED for experimental v1:** cancellation is an advertised no-op because the pinned signed Quack artifact has no `quack_cancel`; server work is not claimed to stop.
- [x] **WAIVED for experimental v1:** abandoned-stream reclamation is not guaranteed and remains an explicitly documented operational risk.
- [x] Confirm memory, thread, and temporary-storage limits are locked and resource/timeout failures map to stable sanitized codes; Quack client deadlines remain observer-only under the accepted cancellation no-op.
- [x] Add `docs/spikes/security-cancellation-gate.md` with reproducible evidence and an explicit PASS/FAIL conclusion.
- [x] Record the project owner's approved no-op cancellation deviation and resume downstream implementation without advertising cancellation support.

### Task 4: Build the reusable QuackRidge service lifecycle

Turn the successful spike into a reusable Go library with explicit dependencies and deterministic lifecycle behavior. The CLI and future SwiftUI supervisor must both use this facade rather than reaching into engine packages.

- [x] Define the public `quackridge` API around context-driven `Start`, `Status`, `Reload`, and `Stop` operations plus immutable option and status types.
- [x] Implement a lifecycle state machine covering stopped, starting, ready, degraded, reloading, stopping, and failed states.
- [x] Move DuckDB ownership behind `internal/engine` and ensure no CLI, control, or adapter package receives raw engine internals unnecessarily.
- [x] Define a source-adapter registry and lifecycle contract that supports validation, attach, metadata, health, and cleanup without PostgreSQL-specific fields in the engine.
- [x] Implement stable typed errors for authentication, protocol mismatch, source unavailable, rejected statement, cancellation, timeout, resource exhaustion, and internal failure.
- [x] Add structured logging with query IDs, timing, source identity, and component fields while redacting credentials and SQL text by default.
- [x] Implement bounded reconnect backoff per source without retrying user queries automatically.
- [x] Make reload transactional: validate new configuration and secrets before replacing healthy source state, and preserve existing state on failure.
- [x] Add deterministic concurrent lifecycle tests, cleanup tests, and race-detector coverage for start/reload/stop/query interactions.

### Task 5: Implement configuration, credential stores, and the PostgreSQL adapter

Add production source management while keeping secrets out of configuration, shell history, DuckDB persistence, and logs. PostgreSQL must remain read-only through both DuckDB attachment and database-level credentials.

- [x] Define a versioned configuration model containing source IDs, display names, validated aliases, adapter types, non-secret connection fields, enabled state, and migration version.
- [x] Store configuration in platform application-config directories with restrictive permissions, atomic replacement, backup-on-migration, and recovery tests for truncated writes.
- [x] Define the credential-store interface and implement macOS Keychain, Windows Credential Manager, and Linux Secret Service providers behind build-tagged or platform-specific packages.
- [x] Add an explicit environment/in-memory credential provider for CI and headless testing only; never silently fall back to plaintext files.
- [x] Model PostgreSQL host, port, database, user, SSL mode, root certificate reference, and connection options separately so a credential-bearing DSN is never persisted.
- [x] Implement `source add` validation that checks alias safety, duplicate IDs/aliases, SSL requirements, credential availability, and bounded connectivity before persistence.
- [x] Implement the PostgreSQL adapter using `ATTACH ... (TYPE postgres, READ_ONLY)` and a dedicated read-only database role.
- [x] Verify startup transactions are read-only and make `doctor` warn about obvious role attributes or grants that conflict with the documented security model.
- [x] Implement the stable v1 metadata relation from the protocol schema, including degraded/unavailable sources without collapsing healthy catalogs.
- [x] Add disposable PostgreSQL integration tests for schemas, tables, views, nullability, arrays, decimals, timestamps, joins, SSL options, timeouts, disconnects, and denied writes.
- [x] Ensure tests never inspect or mutate an existing developer database or credential store.

### Task 6: Implement the CLI, local control API, and browser pairing

Expose the service through a human-friendly CLI and a versioned local management interface suitable for the future menu-bar app. Browser pairing is the only temporary HTTP management surface; all persistent management stays on local IPC.

- [x] Implement `quackridge source add|list|test|remove`, `serve`, `status`, `doctor`, `pair`, and `version` commands using the public service facade.
- [x] Read passwords and tokens through a masked prompt or standard input, never a password command-line flag; add tests proving secrets do not appear in arguments or logs.
- [x] Support stable human output plus explicit `--json` output for automation and the future GUI.
- [x] Run `serve` in the foreground by default with correct signal handling, deterministic exit codes, graceful query draining, and forced bounded shutdown.
- [x] Implement a versioned control API over Unix-domain sockets on macOS/Linux and named pipes on Windows with local-user access restrictions.
- [x] Expose configuration, source health, lifecycle, token rotation, version, capabilities, and diagnostics over control IPC without exposing raw secrets.
- [x] Implement `pair` as a short-lived loopback HTTP exchange with a single-use high-entropy nonce, strict origin allowlist, expiry, replay protection, and immediate shutdown after success or timeout.
- [x] Return only Quack endpoint, QuackRidge identity, capabilities, and the current Quack token from a successful pairing exchange.
- [x] Add manual URI/token display as an explicit development/self-hosted fallback.
- [x] Add unit and integration tests for IPC permissions, malformed messages, nonce replay, disallowed origins, expiry, token rotation, signal handling, and concurrent status calls.

### Task 7: Stabilize the QuackRidge query and metadata protocol

Convert the spike's behavior into a compatibility-tested v1 contract. PondPilot must be able to distinguish QuackRidge from a generic Quack server and fail closed when versions or capabilities do not match.

- [ ] Finalize QuackRidge identity fields and capabilities published through `quack_identify`, including protocol version, product version, source types, read-only mode, metadata version, and cancellation support.
- [ ] Finalize the QuackRidge metadata relation name and exact Arrow/DuckDB types from `protocol/v1` fixtures.
- [ ] Preserve sticky server sessions for an attached Quack client so statements in one PondPilot script tab retain transaction and temporary-object state.
- [ ] Define how PondPilot query IDs are attached to server statements and correlated with Quack and engine logs without changing SQL semantics.
- [ ] Prefix or map server failures to stable QuackRidge error codes while preserving sanitized, actionable context for PondPilot.
- [ ] Add compatibility tests for supported, older, newer, and missing protocol/capability identities.
- [ ] Add regression tests showing a multi-table statement succeeds through `catalog.query(...)` and fails if incorrectly decomposed into simultaneous remote scans.
- [ ] Add result tests for nested values, decimals, timestamps/time zones, intervals, UUIDs, nulls, empty results, duplicate column names, and large streaming batches.
- [ ] Add version-pair documentation and refuse untested DuckDB/Quack combinations at startup.

### Task 8: Add QuackRidge discovery, installation, and pairing to PondPilot

Represent QuackRidge as a distinct PondPilot data-source type that reuses generic Quack attachment primitives but has different execution semantics. Add an installation and pairing flow without allowing the browser to silently install native code.

- [ ] Import or sync the pinned `protocol/v1` schemas and fixtures into PondPilot with a script that records the originating QuackRidge tag and fails on unreviewed drift.
- [ ] Add a `QuackRidgeConnection` model with endpoint, alias, protocol version, capabilities, connection state, timestamps, and an encrypted secret reference; do not duplicate the Quack token in persisted plain state.
- [ ] Reuse hardened Quack extension loading, URI validation, token resolution, attach/detach, timeout, and reconnect helpers where their semantics match.
- [ ] Fetch and validate the signed QuackRidge release manifest, including protocol range, channel, platform assets, hashes, signatures, and minimum OS versions.
- [ ] Add conservative platform detection for macOS AMD64/ARM64, Linux AMD64, and Windows AMD64 with a visible manual alternative selector.
- [ ] Add an install screen explaining that the browser downloads but cannot launch/install QuackRidge, linking only to manifest-validated signed assets.
- [ ] Add the one-time pairing flow with strict origin behavior, expiry handling, manual URI/token fallback, and useful recovery instructions.
- [ ] Identify QuackRidge through `whoami()` after attach and reject generic or incompatible Quack servers from the QuackRidge flow.
- [ ] Persist the token through PondPilot's encrypted secret store and restore/reconnect without placing it in URLs, logs, notifications, or analytics.
- [ ] Add unit tests for manifest validation, platform selection, identity/capability negotiation, persistence, reconnect, expiry, and sanitized failures.

### Task 9: Route PondPilot queries and metadata through QuackRidge

Add the explicit execution target that solves the multi-streaming-scan limitation. Each validated PondPilot statement must execute as one server-side QuackRidge query while retaining existing editor behavior and per-tab session pinning.

- [ ] Add QuackRidge to script-session target selection and make the active target visually explicit in the editor toolbar.
- [ ] Continue using PondPilot's existing statement splitting and validation, then safely quote and send each complete allowed statement through the attached catalog's `query(...)` macro.
- [ ] Use the tab's pinned DuckDB-WASM connection so Quack's attached server session remains stable across statements, transactions, cancellation, and temporary objects.
- [ ] Prevent bridge-targeted statements from using direct remote table scans internally and reject unsupported browser-local/QuackRidge cross-engine queries with a clear v1 limitation.
- [ ] Query the QuackRidge metadata relation for Data Explorer instead of relying on `current_database()` or generic DuckDB internal catalog inference.
- [ ] Route table previews, row counts, column summaries, statistics, and exports as complete server-side statements through the same execution contract.
- [ ] Map QuackRidge error codes to existing query error UI without exposing credentials, raw connection strings, or internal filesystem paths.
- [ ] Advertise `cancellation_noop`, disable the editor cancellation action for QuackRidge, and continue showing timed-out, disconnected, and resource-limited states distinctly.
- [ ] Ensure generic Quack connections retain their current behavior and tests.
- [ ] Add unit tests for statement wrapping/escaping, target selection, metadata mapping, capability failures, cross-engine rejection, error mapping, and cancellation state.

### Task 10: Build cross-repository end-to-end and compatibility testing

Exercise the actual browser-to-QuackRidge-to-PostgreSQL path with no mocked data plane. These tests are the evidence that credentials stay local, joins execute server-side, metadata renders, cancellation works, and cleanup is complete.

- [ ] Add a QuackRidge test harness that starts a disposable PostgreSQL container, creates read-only fixtures, allocates free loopback ports, starts the built binary, and waits on identity/health readiness.
- [ ] Add PondPilot Playwright helpers that pair with the real process without embedding static credentials in screenshots, traces, or test reports.
- [ ] Test installation-manifest selection separately from native installation so CI never executes an untrusted downloaded binary.
- [ ] Test successful pairing, source appearance, complete schema/table/column trees, previews, single-table queries, multi-table joins, exports, and browser reload/reconnect.
- [ ] Test wrong/expired/replayed pairing nonces, wrong Quack token, incompatible protocol, unavailable PostgreSQL, partial-source failure, and token rotation.
- [ ] Test that QuackRidge visibly disables cancellation and never claims server-side reclamation while `cancellation_noop` is advertised.
- [ ] Test denied DML, DDL, attach, extension, filesystem, nested pass-through, and configuration statements from the browser.
- [ ] Test large streamed results with bounded process/browser memory and abandon a result mid-stream to verify cleanup.
- [ ] Verify test teardown leaves no QuackRidge/PostgreSQL processes, containers, ports, temporary files, or credential entries.
- [ ] Add PondPilot CI that builds or downloads a pinned QuackRidge artifact and runs the dedicated suite on Linux AMD64.
- [ ] Add scheduled compatibility CI against the newest QuackRidge prerelease without allowing failures to silently update the pinned supported version.

### Task 11: Package, sign, and publish QuackRidge artifacts

Produce self-contained, verifiable release assets for the approved v1 platforms. PondPilot consumes only the signed manifest and must never construct download URLs independently.

- [ ] Build release binaries for macOS AMD64, macOS ARM64, Linux AMD64, and Windows AMD64 with reproducible version metadata.
- [ ] Bundle the exact matching signed `postgres` and `quack` extension artifacts, their upstream licenses, and recorded SHA-256 hashes beside each binary.
- [ ] Make startup load only the bundled extension directory and work without downloading executable code.
- [ ] Generate archives, checksums, provenance, licenses, and an SBOM through a pinned release toolchain.
- [ ] Sign release artifacts and the release manifest using the project's approved signing mechanism; document key rotation and verification.
- [ ] Define the release manifest from `protocol/v1`, including version, release channel, protocol range, assets, OS/architecture, minimum OS, hashes, signatures, and download URLs.
- [ ] Publish the manifest both as a release asset and at the stable PondPilot-controlled URL consumed by the browser.
- [ ] Add native smoke jobs for macOS AMD64/ARM64, Linux AMD64, and Windows AMD64 that start QuackRidge, query identity, attach a test source where supported, and stop cleanly.
- [ ] Block publishing any platform asset whose native smoke test did not run and pass.
- [ ] Add initial installation paths for signed archives plus Homebrew and Windows package-manager manifests; keep service installation and automatic updates out of v1.
- [ ] Test the exact downloaded release archive in the cross-repository PondPilot suite before promoting a prerelease.

### Task 12: Complete diagnostics, documentation, and the experimental v1 release

Close the feature with user-facing operational guidance and mechanically verified acceptance evidence. Release QuackRidge before enabling its dependent PondPilot UI so the browser never advertises an unavailable compatible artifact.

- [ ] Complete `quackridge doctor` checks for version compatibility, extension hashes, loopback ports, credential-store access, source connectivity/read-only posture, active limits, and stale process/socket cleanup.
- [ ] Document installation, CLI commands, configuration locations, credential behavior, PostgreSQL read-only role setup, SSL modes, pairing, token rotation, troubleshooting, and complete uninstall steps.
- [ ] Document the security model, trusted-input boundary, denied SQL surface, resource limits, local logging/redaction, and how to report vulnerabilities.
- [ ] Document known Quack beta limitations, the supported DuckDB/Quack version pair, v1 non-goals, and the cancellation/security go/no-go evidence.
- [ ] Add PondPilot help content for download, installation, pairing, source health, reconnect, incompatibility, cancellation, and removal.
- [ ] Run every Validation Command and record platform smoke-test links and cross-repository end-to-end evidence in the release checklist.
- [ ] Verify no passwords, DSNs, tokens, SQL text, temporary credentials, or private database contents appear in config files, process arguments, logs, screenshots, traces, SBOMs, or release assets.
- [ ] Publish a signed QuackRidge `v0.1.0` prerelease and stable manifest only after all go/no-go and platform gates pass.
- [ ] Pin that QuackRidge release and protocol contract in PondPilot, run the full PondPilot CI suite, and release the browser integration behind an experimental label.
- [ ] Update the approved design status and compatibility documentation with the shipped versions and any explicitly accepted deviations.
- [ ] Leave write access, cross-engine joins, remote Quack exposure, multi-user sharing, service installation, automatic updates, the SwiftUI app, and non-PostgreSQL adapters for separately approved plans.
