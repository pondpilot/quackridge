# Plan: QuackRidge for macOS

Status: proposed for review
Date: 2026-08-14

Build a native macOS application that makes QuackRidge's complete everyday workflow usable without Terminal while retaining the CLI as the authoritative backend and automation interface. The application will live in the menu bar, provide a full manager window, supervise a bundled `quackridge` process, and communicate with it over a versioned local management protocol.

The first release targets macOS 13 or newer on Apple Silicon and Intel. It will be distributed outside the Mac App Store as signed and notarized, architecture-specific `.dmg` files. Closing the manager window leaves the menu-bar application and bridge running. Quitting the application stops only the daemon process owned by that application. Launch at Login is offered during onboarding and remains opt-in.

## Approved product decisions

- Use a native SwiftUI menu-bar application with a conventional manager window.
- Cover the complete everyday workflow: onboarding, service lifecycle, source add/test/edit/enable/remove, pairing, health, diagnostics, token rotation, logs, and launch-at-login.
- Bundle and supervise the existing Go CLI instead of duplicating the engine in Swift or invoking a new subprocess for every action.
- Extend the user-only Unix-socket protocol so the reusable Go management layer remains the sole owner of validation, configuration, Keychain credentials, runtime reconciliation, pairing, and diagnostics; Swift never reimplements those responsibilities.
- Distribute a signed and notarized direct-download application rather than a sandboxed Mac App Store build.
- Keep the CLI independently usable for automation, recovery, Linux, and advanced troubleshooting.

## Goals and acceptance criteria

- A new user can install QuackRidge, add a supported source, confirm its health, start the bridge, and pair PondPilot without opening Terminal.
- Routine state is visible from the menu bar in one glance; configuration and recovery remain available in the manager window.
- Passwords, Quack tokens, credential-bearing connection strings, and private database contents never appear in process arguments, configuration files, logs, crash reports, analytics, app-generated test/support captures, or support exports. The sole deliberate token presentation is guarded manual recovery: after authentication it may appear briefly on screen or on the clipboard, and the warning states that macOS/user screen capture cannot be reliably prevented. Non-secret ODBC DSN names may remain in source configuration but are excluded from diagnostics and support exports.
- Source mutations are serialized across daemon and CLI processes and crash-durable across validation, versioned Keychain changes, atomic configuration persistence, and runtime reload. A failed or interrupted pre-commit change recovers the previous working state; a committed change keeps its verified candidate authoritative while non-critical cleanup retries separately.
- The application and bundled daemon have an exact, machine-checked management-protocol and product-version relationship.
- The application recovers predictably from daemon crashes, stale sockets, incompatible external processes, locked Keychain access, invalid extensions, and unavailable sources without entering a restart loop.
- Both app architectures pass native build, signing, notarization, Gatekeeper, launch, management, source, pairing, and shutdown smoke tests using the exact packaged artifact.
- VoiceOver, keyboard navigation, reduced motion, increased contrast, light/dark appearance, and Dynamic Type-like macOS accessibility sizing are verified for every primary workflow.

## Non-goals for the first app release

- Mac App Store distribution or App Sandbox support.
- Embedding Go as a framework or moving engine logic into Swift.
- A daemon that survives choosing **Quit QuackRidge**; that would require an independently managed service and was explicitly not selected.
- Automatic installation of ODBC drivers, database certificates, or system dependencies.
- Editing database roles, permissions, or server configuration.
- Query editing, result browsing, or duplicating PondPilot inside the native app.
- In-app update checking or automatic self-update. Help may open the stable QuackRidge releases page, but authenticated update discovery and installation require a separate trust and updater design.
- Telemetry or analytics. Operational logs remain local and redacted.
- Windows or Linux GUI applications.

## User experience

### Visual direction

The visual direction is a **quiet infrastructure console**: native, calm when healthy, and progressively detailed when action is required. Use standard macOS materials and controls, SF Pro and monospaced system typography where appropriate, a graphite-and-fog neutral palette, PondPilot teal for primary actions, amber for degraded states, and red only for actionable failures. Do not use color as the only state signal.

The app icon and menu-bar template icon should share a restrained ridge motif. A small ridge-shaped activity mark may animate while the service starts or reloads; it must stop in steady state and honor Reduce Motion. The application should feel at home on macOS rather than imitate a web dashboard.

### Menu-bar surface

The menu-bar popover is the daily control surface. It contains:

- Current lifecycle state, endpoint, uptime, and backend version.
- Healthy, degraded, and disabled source counts.
- The single most important active warning with a direct recovery action.
- **Open Manager**, **Pair with PondPilot**, and a context-sensitive Start, Retry, or Restart action.
- **Launch at Login**, Help, and **Quit QuackRidge** in a secondary menu.

Use a monochrome template icon whose silhouette or badge shape distinguishes stopped, starting, healthy, and degraded states. Do not rely on unsupported menu-bar color behavior.

### Manager window

Use a native sidebar with five destinations:

1. **Overview** — service health, source summary, pairing readiness or recent outcome, endpoint, uptime, versions, and the most useful next action.
2. **Sources** — searchable source list, health and enabled state, detail inspector, and guided add/edit flows.
3. **Pairing** — one-time challenge, expiry progress, copy/open actions, success state, and guarded manual recovery.
4. **Diagnostics** — grouped checks, actionable fixes, local logs, and sanitized support-report export.
5. **Settings** — Launch at Login, service behavior, advanced paths, versions, licenses, and token rotation.

First launch presents a short onboarding flow that explains the local bridge and credential boundary, offers Launch at Login without preselecting it, starts the bundled daemon, and ends with **Add a Source** or **Pair with PondPilot**. Returning users go directly to Overview.

### Source workflow

The add flow is a short wizard: choose source type, enter only relevant fields, test the connection, review the read-only posture, then save and activate. PostgreSQL, MySQL/MariaDB, SQLite, DuckDB, and ODBC all receive first-class forms. SQLite and DuckDB use `NSOpenPanel`; ODBC supports DSN or driver, reviewed public properties, and Keychain-backed secure custom properties.

Editing starts from a copy of the persisted non-secret configuration. Existing credentials are represented as “stored in Keychain,” never loaded into Swift. The user may keep, replace, or remove a credential when the connector permits it. Alias changes warn that PondPilot catalog references may need updating. Removal explains that both configuration and the associated Keychain item will be deleted and requires confirmation.

## System architecture

### Application bundle

Each architecture-specific app contains one matching backend and extension set:

```text
QuackRidge.app/
  Contents/
    MacOS/QuackRidge
    Helpers/quackridge
    Resources/Backend/extensions/*
    Resources/Licenses/*
    Resources/backend-manifest.json
```

The app and backend use the same release version. `backend-manifest.json` records the backend architecture, SHA-256 digest, management protocol version, Quack protocol version, DuckDB version, and extension digests. The app verifies this manifest before launch and refuses mixed or modified bundle contents with a specific diagnostic.

Use stable signing identities from the first spike onward:

- App bundle identifier: `io.pondpilot.quackridge`
- Backend helper signing identifier: `io.pondpilot.quackridge.backend`
- Existing Keychain service: `io.pondpilot.quackridge`

Changing any of these identifiers after prerelease requires an explicit credential-migration and login-item compatibility review.

The initial release publishes separate ARM64 and AMD64 DMGs rather than doubling the package size with a universal app containing two native backends and two extension sets. A universal distribution can be evaluated later without changing the runtime contract.

### Process supervision

`ServiceSupervisor` owns a single `Process` instance. It launches the bundled backend with explicit absolute paths for configuration, control socket, and extensions and constructs—not merges—a minimal reviewed environment whose home/user/temp values come from trusted OS account APIs and whose locale values are fixed/validated. It never copies `HOME`, `TMPDIR`, credential-like variables, any `QUACKRIDGE_*` input, `DYLD_*`, `PATH`, or other shell/launchd state from the parent; every executable and resource path is absolute. The same launcher policy applies to offline `doctor`. No credential or token is passed through arguments or environment variables.

Before spawn, the app creates a one-client Unix event listener in its mode-`0700` state directory and passes only its path to the backend. The listener accepts the owned child UID/PID, removes its pathname after connection, and carries bounded typed readiness, lifecycle, structured-log, and offline-result frames written by trusted Go code. Only this channel feeds `AppModel`, `LogStore`, or doctor decoding. Raw child stdout and stderr—including all native driver output—are continuously drained with the same bounded streaming-discard counter used for malformed input and are never parsed, displayed, hashed, or exported. Ordinary interactive CLI invocations retain their terminal output; the private event channel is required only in app-owned mode.

App-owned startup has a sixty-second overall deadline backed by a cancellable daemon startup context. The daemon emits safe phase progress for bundle verification, engine start, source bootstrap, and control publication over the private event channel so the app can show useful progress without treating raw output as readiness. If the deadline expires, the supervisor closes the lifecycle pipe, sends `SIGTERM`, waits up to ten seconds, force-terminates only its owned child if required, runs bounded offline diagnostics, and enters `failed` with Retry. No startup or Keychain operation may leave the app in `starting` indefinitely.

The app keeps a dedicated lifecycle pipe open to the owned child. A new opt-in `serve` flag makes the daemon stop cleanly when that pipe reaches EOF, preventing an orphaned bridge if the menu-bar application crashes. Ordinary foreground CLI behavior remains unchanged.

Unexpected exits receive at most two automatic restart attempts with bounded backoff. Each engine process exposes a fresh non-secret `daemon_instance_id`, and each token exposes an independent non-secret random `pairing_generation` that changes on process start and every token rotation; readiness, handshake, status, and rotation responses carry the applicable values. Pairing outcomes are keyed to both values rather than treated as durable account state. When either value changes, Swift atomically cancels stale pairing polling, invalidates any prior success, and shows **Re-pair required** with a direct action. The UI cannot return to fully healthy/paired solely because a replacement child is ready, nor remain paired after another management client rotates the token. Repeated failure transitions to `failed` and requires a user Retry. Closing the manager window has no process effect. Choosing Quit sends `SIGTERM`, waits up to ten seconds, then force-terminates only the child process owned by this app if necessary.

If the default control socket already belongs to a compatible daemon, the app attaches in a clearly labeled **Externally managed** mode. It does not claim process ownership or stop that daemon on quit. An incompatible listener or non-socket file produces recovery guidance and is never deleted automatically.

### Swift application layers

- `AppShell` owns `MenuBarExtra`, window scenes, activation policy, commands, and deep links.
- `ServiceSupervisor` owns launch, readiness, restart policy, shutdown, and captured backend logs.
- `ManagementClient` is an actor-backed Unix-domain socket client using one request/response connection per operation.
- `AppModel` is the main-actor state coordinator and the only source consumed by views.
- `SourceDraft` and connector-specific form models keep transient input separate from persisted source snapshots.
- `LoginItemController`, `LogStore`, and `SupportReportBuilder` isolate platform integrations.
- Views are feature-grouped under Overview, Sources, Pairing, Diagnostics, Settings, Onboarding, and shared design-system components.

No third-party Swift dependency is required for the first release. Prefer Apple frameworks: SwiftUI, AppKit where SwiftUI lacks a native behavior, ServiceManagement, LocalAuthentication for guarded manual recovery, Security where app-owned metadata is needed, and Foundation. The Go backend remains the only code that reads or writes QuackRidge source credentials.

### Management protocol v2

Promote the local control contract to a cross-language protocol under `protocol/management/v2/` with JSON Schemas and valid/invalid fixtures. This protocol is distinct from the browser-facing Quack protocol under `protocol/v2`.

Every request includes `version`, `request_id`, `operation`, and an operation-specific payload. Every response echoes `request_id` and contains either a typed result or a stable error object with `code`, safe `message`, optional `field`, and optional safe recovery metadata. Readiness, handshake, and status expose the same per-process `daemon_instance_id` plus current `pairing_generation`; `rotate_token` returns the new generation. This lets clients invalidate process- and token-bound state without exposing a credential. Unknown fields, oversized frames, duplicate JSON values, unsupported versions, and unknown operations fail closed.

Required operations are:

- `handshake`, `status`, `configuration`, and `diagnostics`
- `source_test`, `source_add`, `source_update`, `source_remove`, `source_set_enabled`, and `source_refresh`
- `certificate_import`, `certificate_list`, and `certificate_remove`
- `service_reload`, `pair_start`, `pair_status`, `pair_cancel`, `manual_reveal_prepare`, `manual_reveal_consume`, and `rotate_token`

The management server retains the current one-request-per-connection framing. Short operations have five-second deadlines; source validation and reload receive explicit bounded deadlines up to thirty seconds. Poll once per second only while lifecycle state is transitional or a mutation is active, every five seconds while the manager or menu-bar popover is visible and steady, and on a coalesced thirty-second interval while the app is idle in the menu bar. Suspend timers during system sleep and refresh immediately after wake, network change, user action, or surface presentation. A general management-state subscription stream is deliberately deferred until adaptive polling proves insufficient; this is separate from the narrow app-owned child event channel used only for readiness, lifecycle, logs, and offline results.

### Transactional source management

Move connector construction and validation out of `internal/cli` into a reusable Go management service. GUI operations always reach that service through the daemon. A CLI mutation against the default live configuration must use management IPC whenever a compatible daemon is reachable so configuration and runtime change together. If a daemon is live but management-incompatible, the CLI refuses the mutation and instructs the user to stop or upgrade it; it never silently falls back to direct file editing. When no daemon is reachable, the CLI may use the same service offline and the next daemon start loads the committed state.

All online and offline mutations acquire an advisory cross-process lock in the QuackRidge configuration directory. Implement the lock on every supported CLI platform, keep the directory at mode `0700` and lock file at `0600` where modes apply, and hold it through validation, journal creation, config commit, runtime reconciliation, and the immediate cleanup attempt. Once candidate state is committed, a denied superseded-item deletion becomes cleanup debt and releases the mutation lock; each later bounded retry reacquires the same lock. The daemon may also use an in-process mutex for ordering, but that mutex is not the concurrency boundary.

Daemon startup acquires the same lock before journal recovery or configuration load and holds it through source bootstrap and successful control-socket publication. An offline CLI mutation that observed no socket must acquire the lock and probe the control endpoint again before editing. If startup published a compatible daemon while the CLI waited, the CLI releases the lock and routes the mutation through IPC; if it published an incompatible daemon, the CLI refuses the mutation. If the CLI obtained the lock first, it completes its offline transaction before startup loads configuration. This closes the no-socket/startup interval rather than treating a pre-lock reachability check as authoritative.

The migration also moves authoritative state to a new epoch directory, for example `~/Library/Application Support/QuackRidge/state-v2/`, containing config, lock, migration recovery state, active transaction journal, cleanup-debt directory, immutable recovery snapshots plus an atomic committed-head pointer, certificates, and control socket. Before migrating, the app probes the legacy control endpoint and refuses to proceed until any old daemon stops. It prepares a mode-`0600`, durably self-identifying claim record in the same directory—fixed magic/product/schema plus a random attempt ID—fsyncs it, then atomically renames it onto the vacant legacy control pathname with no-replace/no-follow semantics and syncs the directory before reading or staging migration state. If a last-release `serve` wins the pathname race, claim fails and migration aborts/re-probes without unlinking it; if the claim wins, the old server refuses the non-socket path.

The claim remains in place through activation, and its attempt ID plus file identity and phase are immediately recorded and synced in migration recovery state. A crash before that journal write is still recoverable because the fully written claim is self-identifying: with no active-v2 marker, relaunch validates owner, type, mode, magic/schema, attempt ID, and file identity, then either resumes a matching staged migration or rolls back only that verified claim and returns v1 to service. A foreign, malformed, symlink, or identity-changed entry is never deleted. Verified temporary claim files left before rename are handled the same way. After the epoch flip, the claim becomes the permanent control sentinel. Kill tests cover every boundary from temporary-file creation through marker publication.

A Go preflight checks the Homebrew cask receipt plus the standard Apple Silicon and Intel Homebrew binary locations without depending on Finder's restricted `PATH`; any installed `quackridge` it finds must advertise state-epoch support at least as new as the app, or activation blocks with exact upgrade guidance. A small atomic active-epoch marker tells new binaries which state root is authoritative. Migration clones every accessible legacy fixed credential into a new immutable versioned reference, publishes the complete v2 state and marker only after validation and Keychain staging succeed, and never makes v2 depend on a legacy credential reference.

At the epoch flip, migration replaces the known legacy config with a minimal non-secret unsupported-version retirement document while retaining the already-claimed control sentinel. The last released binary therefore rejects normal config operations and cannot bind a competing legacy daemon. New binaries recognize but never delete these sentinels as stale state. A legacy command that loaded v1 before migration may still complete a delayed save against the retired config, but it cannot replace or break v2 and cannot start a server across the control sentinel; the new app restores the known retirement document after detecting that late write without reading, hashing, logging, or importing its contents. Release tests race last-release `serve` after the initial probe but before claim, while the flip is pending, after activation, and against a paused late writer.

Each mutation receives an `expected_revision`, computed from a canonical configuration snapshot. After acquiring the cross-process lock, the service reloads the document and rechecks the revision before any side effect. A mismatch returns a stable conflict and forces the caller to refresh; revision checks alone are not treated as locking.

Credential replacement uses immutable, versioned references such as `quackridge/source/<source-id>/<transaction-id>` instead of overwriting an existing Keychain item. A user keeping an existing credential retains its current reference. The service persists one restrictive, non-secret active transaction journal containing the transaction ID, phase, previous and candidate configuration snapshots, and old/new credential references. The journal and configuration file are atomically replaced and directory-synced. Separately, `cleanup-debt/<transaction-id>.json` holds uniquely named, non-secret post-commit deletion work so debt from multiple commits cannot overwrite the active slot or block later mutations.

For add or update, the service:

1. Parses and validates the strict connector-specific draft.
2. Acquires the cross-process lock, recovers any prior incomplete transaction, reloads config, and rechecks `expected_revision`.
3. Builds and validates the complete candidate configuration.
4. Tests the affected connection using a draft credential or existing Keychain reference.
5. Writes a `prepared` journal and stores any replacement credential under a new immutable reference.
6. Atomically persists the candidate config pointing to the new reference.
7. When online, reloads or transactionally rebuilds the runtime and verifies the resulting source state; when offline, verifies the durable candidate that the next daemon start will load.
8. Writes and syncs an immutable sanitized recovery snapshot keyed by the candidate revision, atomically advances and syncs the small committed-head pointer to that revision/digest, then marks the active journal `committed`; the candidate primary, selected recovery snapshot, runtime, and new credential are now authoritative. The v2 loader accepts a primary only when it matches committed head, otherwise loads exactly the immutable snapshot named by head; it never falls back to the legacy v1 `.bak`, scans for a “latest” snapshot, or guesses after malformed head metadata.
9. Attempts to delete the superseded credential. On denial or transient failure, it atomically renames the committed active journal with no-replace semantics to its transaction-ID cleanup-debt record and syncs both directories, freeing the active slot; on success it removes the journal. Recovery reconciles an already-present same-ID record by verified non-secret record digest rather than overwriting it, so a crash on either side of the rename loses or duplicates no debt.
10. On an ordinary failure before commit, restores the previous primary, committed-head pointer, and runtime, deletes the staged credential and unreferenced candidate snapshot, and removes the journal only after rollback is verified.

Startup and every new mutation recover the active journal under the same lock. An uncommitted journal restores the previous primary, committed-head pointer, runtime, and credential reference before service start; inability to prove that rollback fails closed with an actionable diagnostic. A committed active journal is atomically moved into the cleanup-debt directory before new mutations proceed. Candidate state remains authoritative. Startup and bounded maintenance passes drain only a fixed number of uniquely named debt records per run under the lock; failed deletion emits a non-secret pending-cleanup warning and leaves that record for later without reverting or blocking healthy service and management. Successful deletion removes and directory-syncs only its matching record; recovery snapshot garbage collection keeps every head-, journal-, or debt-referenced revision.

Removal keeps the old credential until the candidate config has reloaded successfully; its committed-journal cleanup deletes the old item. A denied post-commit deletion may leave an unreferenced Keychain item temporarily, but cannot make an already-authoritative config or runtime unavailable. This makes a forced process exit or power loss recoverable without storing credential bytes in the journal. Credential buffers remain byte arrays, are cleared as soon as practical, are never returned over IPC, and are never formatted into errors. Swift minimizes the lifetime of secure-field values and clears form state immediately after submission.

### Certificates and ODBC properties

PostgreSQL `verify-ca` and `verify-full` use a managed certificate store rather than persisting an arbitrary path. The app selects a PEM file with `NSOpenPanel` and submits it to `certificate_import`; the CLI gains an equivalent explicit import command. Go enforces a bounded file size, accepts certificate blocks only, rejects private-key material, validates that the bundle can act as a CA chain, and writes it atomically to a mode-`0700` managed directory under an immutable content-addressed reference such as `quackridge/certificate/<sha256>`. Config persists only that reference. The PostgreSQL factory resolves it to the managed absolute path and supplies `RootCertificatePath` to the adapter. Certificate deletion is allowed only when no committed or journaled configuration references it; unreferenced imports can be garbage-collected safely.

ODBC properties are split into public configuration and Keychain-backed sensitive properties. Go owns a narrow, reviewed allowlist of property names that are safe to persist for each semantic database type. Any unrecognized, authentication-related, or custom-driver property is accepted only through a secure-property input and stored inside the versioned ODBC credential object in Keychain; it is merged into the connection string only in memory. The GUI labels these fields “Stored in Keychain.” CLI secure-property values use masked prompts or standard input and never `key=value` arguments. Property names such as API keys, access keys, client secrets, bearer values, authentication blobs, and vendor-specific unknowns can therefore never fall through to `config.json` merely because a denylist missed their spelling.

### State, errors, and recovery

Expose lifecycle states already used by the Go facade: `stopped`, `starting`, `ready`, `degraded`, `reloading`, `stopping`, and `failed`. Extend source snapshots with enabled state, safe health code, last-check time, and next-retry time. A degraded service remains available when at least one source is healthy.

The daemon owns a bounded live health scheduler; UI polling only observes its results. Ready sources are checked on a coalesced thirty-second cadence with per-check deadlines and jitter. A failed startup attach or live health check records a sanitized unavailable state and schedules source-scoped revalidation and reattachment using the existing bounded backoff without retrying user queries. Successful recovery clears backoff and updates status immediately. Health probes snapshot source identity under the manager lock, perform network work without holding that lock, and apply results only if the source fingerprint/generation is still current. Reload, removal, shutdown, and health reconciliation are serialized at the source-lifecycle boundary. `source_refresh` provides a user-driven bounded check without creating a second health implementation.

Stable management error codes must distinguish validation, authentication, Keychain denial, unavailable host, TLS failure, conflict, timeout, extension mismatch, stale socket, incompatible protocol, pairing expiry, and internal failure. Swift maps field errors inline, source errors to the affected row/detail view, and lifecycle failures to a global recovery surface. Technical details remain behind disclosure and must already be sanitized by Go.

If the daemon cannot start, the app may invoke the bundled doctor once as a read-only offline recovery path. This is the only routine per-command CLI invocation from Swift. It uses the same minimal environment and returns its bounded typed result over a private one-client channel; raw stdout/stderr is discarded and cannot impersonate the result. Recovery actions may reveal the app-owned log directory, open the relevant settings screen, retry startup, or copy a sanitized support report; they do not mutate or delete unknown files automatically.

### Logs and privacy

`LogStore` accepts structured redacted log frames only from the private child-PID-verified event channel, adds app lifecycle events without secrets, and rotates user-only log files to a small fixed budget. Native libraries cannot cause ordinary stdout/stderr writes to enter that channel. Every trusted frame has a strict 64 KiB limit and exact schema; an invalid event channel fails closed rather than falling back to raw output.

Raw backend stdout and stderr are always untrusted. The supervisor continuously drains both with a strict 64 KiB per-record memory limit but never parses or retains their payloads, even when an under-limit line exactly matches a valid structured-log schema. Once a record crosses the limit, it stream-discards through the next delimiter or EOF, retains no further chunks, never hashes or derives metadata from them, and counts the entire record once. Only a rate-limited count and fixed warning with no payload-derived metadata survive. Unterminated input remains memory-bounded and is counted once on limit/EOF while the pipe continues draining.

Support export contains app/backend versions, architecture, protocol versions, lifecycle history, extension verification, sanitized diagnostics, source IDs/types/health, and bounded redacted logs. It excludes source options that may reveal private hosts or paths by default; the export preview tells the user exactly what will be shared. No automatic upload is implemented.

### Guarded manual recovery

Manual URI/token recovery uses a separate management contract and Swift model that normal pairing, `AppModel`, diagnostics, logging, and support export cannot decode. `manual_reveal_prepare` returns a single-use authorization nonce, consequence text, and a thirty-second expiry but no token. The Advanced UI requires explicit confirmation plus successful device-owner authentication through `LocalAuthentication`, then sends the nonce and exact confirmation to `manual_reveal_consume`. The daemon consumes the nonce once and returns the current endpoint and token in a dedicated sensitive result; expiry, replay, cancellation, or failed confirmation returns no secret.

Only the manual-recovery view model holds the result, never in persistent app state or a debug description. The value is concealed by default, auto-hides and clears from memory after thirty seconds or when the view closes, and is excluded from accessibility announcements and app-generated test/support capture artifacts. The disclosure explicitly warns that macOS or user-initiated screenshots and recordings cannot be blocked reliably while the raw value is visible.

Copy is an explicit action tracked by pasteboard change count plus exact content. The sixty-second timer clears only an unchanged copied value. View close and every normal app-termination path synchronously apply the same guard before clearing token memory, so copy→close and copy→Quit do not strand the value and never destroy newer clipboard contents. The warning states that a crash, force quit, or system termination cannot guarantee clipboard cleanup. CLI manual recovery uses the equivalent backend authorization boundary rather than creating an undocumented Swift subprocess path.

### Launch at Login

Use `SMAppService.mainApp` on macOS 13+ for Launch at Login. Registration is opt-in, visible in Settings, and reconciled with the operating system's actual state. If macOS denies or requires approval, the UI links to the correct System Settings location and remains truthful about the current status.

## Security and distribution model

- The app is not sandboxed, but Hardened Runtime is enabled and entitlements are kept minimal.
- The management socket directory is mode `0700` and socket is mode `0600`; peer ownership is verified where the platform exposes it.
- The backend loads extensions only from the verified read-only app bundle path and retains all existing DuckDB extension and filesystem restrictions.
- The signing design must account for both bundled DuckDB extensions and external unixODBC/vendor driver dylibs under Hardened Runtime. Test without an exception first; use `com.apple.security.cs.disable-library-validation` only on the backend helper if required and if the exact packaged/notarized helper can still pass the full ODBC smoke. The GUI executable must not receive it. If no notarizable configuration loads representative external drivers on both architectures, remove/defer ODBC from the first app release rather than ship an unusable form.
- Nested code is signed inside-out, then the app is signed, notarized, and stapled. That exact app is packaged into a separately notarized and stapled DMG, and only the mounted copy from that final DMG is used for Gatekeeper and release smoke testing.
- Existing Keychain service/account identifiers remain stable so users upgrading from CLI archives do not lose credentials. New macOS CLI archives reuse the exact pre-signed helper binary from the app build, with the same Developer ID team, stable signing identifier, entitlements, and reviewed Keychain access policy, rather than shipping an independently identified or unsigned build.
- The spike tests access in both directions: the signed helper reads credentials created by the previously distributed CLI, and the standalone signed CLI reads credentials created or replaced by the app helper, both offline and when serving. If macOS requires authorization, interactive surfaces explain the system prompt. Headless access never hangs on UI: it returns a specific interaction-required/denied error and preserves the item. An inaccessible source stays configured but degraded and offers per-source credential re-entry through a new versioned reference; neither binary deletes or replaces an old item merely because access was denied.
- Manual token display is hidden behind an explicit warning, authentication/confirmation where appropriate, and a short-lived presentation excluded from app-generated capture/support artifacts. The UI states plainly that system/user screenshots or recordings cannot be prevented while the value is revealed.
- The Swift GUI process performs no direct network requests; opening Help delegates the releases URL to the user's browser. The Go backend makes only expected connections to configured database endpoints for validation, attachment, live health, and queries, plus loopback Quack and short-lived loopback PondPilot pairing traffic. It never downloads executable code or performs update checks.

## Implementation sequence

### Task 0: Prove signing, Hardened Runtime, and packaged extension loading

Resolve the highest-risk packaging seam before building application features. Produce a minimal signed host app that launches the exact Go backend and loads the exact bundled extensions from a path containing spaces.

- [ ] Create a disposable macOS signing spike without changing the existing release assets.
- [ ] Build ARM64 and AMD64 backend binaries with the production CGO and DuckDB settings.
- [ ] Set and verify the backend's macOS 13 deployment target and inspect its Mach-O load commands so the Go helper cannot acquire a newer runtime dependency silently.
- [ ] Place each architecture's verified extensions in a minimal `.app` and launch `doctor` plus an engine identity smoke test from the bundle.
- [ ] Test Hardened Runtime with no library-validation exception first; if extension loading fails, test the exception only on the backend helper and document why it is required.
- [ ] From the exact signed helper inside the candidate app, load unixODBC plus representative Homebrew/vendor PostgreSQL and MySQL ODBC drivers and execute disposable read-only queries on ARM64 and Intel; include differing/ad-hoc Team ID cases and real installed dylib paths.
- [ ] Repeat the external-driver smoke after outer-app signing/notarization and from the mounted candidate DMG; if no minimal notarizable entitlement set works on both architectures, explicitly defer ODBC UI/release support.
- [ ] Verify any macOS code-signing step does not invalidate DuckDB's upstream extension signature or QuackRidge's extension digest checks.
- [ ] Verify launch from an app path and user directory containing spaces and non-ASCII characters.
- [ ] Sign one production helper artifact with the proposed stable identifier and reviewed Keychain access policy, reuse that exact signed artifact in both the app and standalone macOS CLI archive, and verify its code requirement and entitlements in both locations.
- [ ] Test both Keychain directions: signed helper reading entries created by the previously distributed CLI, and standalone signed CLI reading entries created/replaced by the app helper in offline and `serve` modes.
- [ ] Record prompt, denial, locked-Keychain, and non-interactive/headless behavior; prove interaction-required fails promptly, the degraded-source credential re-entry path works, and no inaccessible item is deleted automatically.
- [ ] Record the selected entitlements, signing order, bundle layout, and a PASS/FAIL conclusion in `docs/spikes/macos-app-packaging.md`.
- [ ] Stop app implementation if the exact packaged extension set cannot pass under a notarizable configuration; approve a revised helper architecture before continuing.

Validation commands after this task:

```sh
make macos-packaging-spike
make macos-odbc-packaging-spike
codesign --verify --strict --verbose=2 build/QuackRidge.app/Contents/Helpers/quackridge
codesign --verify --strict --verbose=2 build/QuackRidge.app
codesign -d --entitlements :- build/QuackRidge.app/Contents/Helpers/quackridge
build/QuackRidge.app/Contents/Helpers/quackridge doctor --json --extensions build/QuackRidge.app/Contents/Resources/Backend/extensions
```

### Task 1: Define and test management protocol v2

Create the stable protocol contract and Go server boundary before feature logic. Swift conformance follows immediately after the deterministic Xcode scaffold exists in Task 4.

- [ ] Add JSON Schemas for the envelope, handshake, status, per-process daemon identity, independent pairing generation, configuration, source drafts, source mutations, certificate management, source refresh, pairing lifecycle, isolated guarded manual reveal, diagnostics, and structured errors under `protocol/management/v2/`.
- [ ] Add valid and invalid fixtures for every request and response, including omitted secrets, unknown fields, malformed credentials, conflicts, and version mismatch.
- [ ] Add `request_id`, typed payload/result envelopes, strict size limits, operation-specific timeouts, and exact version negotiation to `internal/control`.
- [ ] Preserve local-user socket permissions and reject a non-socket path without deleting it.
- [ ] Add stable management error codes without exposing error causes or raw connector options.
- [ ] Make existing CLI control calls use the new protocol so the daemon has only one management implementation.
- [ ] Specify live-daemon detection and require mutation commands to use management IPC when the default configuration is active; reject rather than edit behind an incompatible live daemon.
- [ ] Document compatibility rules: the app requires an exact management major version and a supported backend product version; the browser-facing Quack protocol remains independent.

Validation commands after this task:

```sh
go test ./protocol/... ./internal/control/...
go test -race ./internal/control/...
```

### Task 2: Build transactional backend source management

Make the daemon a complete, safe management authority and remove connector-specific orchestration from the CLI layer.

- [ ] Introduce a reusable connector registry for strict draft parsing, defaults, validation, credential requirements, and adapter creation.
- [ ] Refactor `source add` and `source test` to call the registry without changing supported CLI syntax or secret input behavior.
- [ ] Implement add, test, update, remove, and enable/disable operations through one online/offline management service.
- [ ] Add an advisory cross-process configuration lock on macOS, Linux, and Windows; recheck configuration revision only after acquiring it and hold it through runtime reconciliation and the immediate cleanup attempt, then release it for non-blocking committed cleanup debt whose retries reacquire the lock.
- [ ] Hold that lock from daemon recovery/config load through control-socket publication, and make offline CLI mutations re-probe the socket after acquiring it so startup and fallback cannot cross.
- [ ] Support testing an edit with either the existing Keychain credential or an explicitly supplied replacement.
- [ ] Replace in-place credential updates with immutable versioned Keychain references.
- [ ] Add one atomically written, directory-synced, non-secret active transaction journal with prepared and committed phases plus deterministic startup recovery.
- [ ] Before marking committed, write an immutable candidate recovery snapshot and atomically advance a synced committed-head pointer to its revision/digest; load only the primary or exact snapshot selected by head, never the legacy v1 `.bak`, directory order, or a guessed revision.
- [ ] Treat an uncommitted rollback failure as blocking, but atomically move a committed superseded-credential deletion failure into a unique durable cleanup-debt record, freeing the active journal for later mutations; drain a bounded number under the lock and retry safely.
- [ ] Coordinate versioned Keychain references, atomic config persistence, and runtime reload with complete rollback or roll-forward cleanup on every failure and crash boundary.
- [ ] Preserve the last healthy runtime and config if validation, Keychain access, persistence, engine rebuild, or source attach fails.
- [ ] Implement the bounded daemon-side health scheduler, generation-safe result application, source-scoped revalidation/reattachment, backoff, manual refresh, and cancellation during reload/removal/shutdown.
- [ ] Return sanitized source health snapshots including enabled state, last-check time, and next-retry time.
- [ ] Add concurrency tests for simultaneous daemon and offline CLI processes, two offline CLI processes, stale revisions, repeated submissions, cancellation, and lock release after process death.
- [ ] Add process-kill and phase-by-phase recovery tests proving no config points to a partially replaced or deleted credential after restart.
- [ ] Deny deletion after two or more sequential commits and prove restart/later mutations remain healthy, each cleanup debt record is visible without secrets, crash recovery around journal-to-debt rename loses nothing, and all records drain after access returns.
- [ ] After update and removal commits complete cleanup, corrupt the primary config and prove restart follows committed head to the candidate snapshot, never a stale reference whose Keychain item was deleted; corrupt head and prove recovery fails closed rather than guessing.
- [ ] Add platform tests proving credential values do not appear in config, logs, error strings, JSON responses, or process arguments.
- [ ] Implement the managed content-addressed CA certificate store, strict PEM/private-key validation, reference resolution into `postgres.Credential.RootCertificatePath`, reference-safe removal, and CLI import/list/remove commands.
- [ ] Split ODBC public properties from secure properties, enforce database-type public allowlists, store every unknown or credential-class property in the versioned Keychain credential, and add secure CLI input.
- [ ] Bump the persisted configuration format before introducing versioned credentials or split ODBC properties so previously released CLIs reject the new document as unsupported instead of mutating it without IPC or locking.
- [ ] Publish migrated state in a new canonical epoch directory behind an atomic active-epoch marker; after activation, never read or merge the retired v1 path, even if an already-running legacy CLI recreates it.
- [ ] Gate app activation on standalone CLI state-epoch support by checking Homebrew receipts and standard ARM64/Intel install locations in Go rather than relying on Finder's `PATH`; after probing, install a fully written self-identifying legacy-control claim with atomic no-replace/no-follow rename and retain it as the sentinel through activation so a racing last-released `serve` cannot win unnoticed.
- [ ] Persist and sync migration attempt/claim identity and phase immediately; on restart with no active marker, resume or roll back only a verified owned claim/temp file, leave foreign collisions untouched, and restore v1 service after abandoned pre-flip work.
- [ ] Implement a startup-only, lock-held migration that refuses to cross a live legacy daemon, clones every accessible fixed credential into a versioned reference, retains reviewed public ODBC keys, moves every unknown or credential-class ODBC value into the new credential, and atomically publishes the sanitized candidate only after all Keychain writes succeed.
- [ ] Keep legacy migration crash-safe without copying or hashing raw property values in the transaction journal: record only phases, paths, credential references, and digests derived exclusively from sanitized v2 state; leave v1 untouched on pre-flip failure; complete forward after the atomic v2 flip; remove any legacy secret-bearing backup after commit.
- [ ] If Keychain is locked or denied, leave the v1 document byte-for-byte authoritative, remove staged references, return a specific recoverable migration error, and let the app guide unlock/retry or secure credential re-entry without starting on a partially migrated config.

Validation commands after this task:

```sh
go test ./internal/config/... ./internal/source/... ./internal/cli/... ./internal/control/...
go test -race ./internal/config/... ./internal/reconcile/... ./internal/control/...
go test -tags=integration ./internal/engine/... ./internal/source/...
```

### Task 3: Add app-owned daemon lifecycle support

Extend `serve` narrowly so a Swift supervisor can own it without changing normal terminal usage.

- [ ] Add an app-owned private Unix event channel that validates the expected child UID/PID and carries bounded readiness, progress, structured-log, and offline-result frames separately from raw stdout/stderr.
- [ ] Add explicit readiness JSON on that channel containing PID, fresh `daemon_instance_id`, current non-secret `pairing_generation`, lifecycle state, endpoint, control path, product version, and management protocol version, with no token; expose both generations from handshake and status.
- [ ] Add safe structured startup phases plus an app-supplied sixty-second startup context that bounds bundle verification, engine start, source bootstrap, Keychain waits, and control publication.
- [ ] Add the opt-in lifecycle-pipe mode that exits gracefully on parent EOF.
- [ ] Keep signal handling, foreground behavior, and existing `serve` invocations backward compatible.
- [ ] Distinguish a live compatible socket, stale socket, non-socket collision, permission failure, and incompatible daemon.
- [ ] Ensure startup failure closes listeners, removes only self-owned sockets, clears pairing servers, and leaves no child process.
- [ ] Add deterministic tests for readiness, every startup-phase timeout, supervisor termination escalation, parent crash/EOF, normal quit, bounded shutdown, and repeated start attempts.
- [ ] Add a real macOS smoke harness that launches and terminates the backend exactly as Swift will.

Validation commands after this task:

```sh
go test ./internal/cli/... ./internal/control/...
go test -race ./internal/cli/... ./internal/control/...
go test -tags=integration -run 'TestServe|TestLifecycle' ./internal/cli/...
```

### Task 4: Scaffold the native macOS application and design system

Create a production Xcode project and the complete static application shell before wiring backend mutations.

- [ ] Add `macos/QuackRidge.xcodeproj` with app, unit-test, and UI-test targets; commit deterministic shared schemes and build settings.
- [ ] Generate strict Swift management-protocol value types from, or audit them against, the checked-in schemas and add valid/invalid fixture-decoding tests before connecting to a live daemon.
- [ ] Target macOS 13+, enable Hardened Runtime, disable App Sandbox, and define Release signing settings without committing credentials.
- [ ] Configure an accessory/menu-bar application with `MenuBarExtra`, a reusable manager `WindowGroup`, native commands, and correct activation when opening the window.
- [ ] Implement the sidebar destinations, empty/loading/error states, window restoration, and Settings scene.
- [ ] Define semantic color, spacing, typography, iconography, material, and motion tokens that work in light, dark, increased-contrast, and reduced-motion environments.
- [ ] Create production app and template menu-bar icon assets based on the ridge motif, including all required sizes and accessibility labels.
- [ ] Add preview fixtures for stopped, starting, healthy, degraded, failed, no-source, and externally managed states.
- [ ] Establish localization-ready strings and avoid constructing user-facing sentences from fragments.

Validation commands after this task:

```sh
xcodebuild build -project macos/QuackRidge.xcodeproj -scheme QuackRidge -destination 'platform=macOS'
xcodebuild test -project macos/QuackRidge.xcodeproj -scheme QuackRidge -destination 'platform=macOS' -only-testing:QuackRidgeTests/ProtocolFixtureTests
xcodebuild test -project macos/QuackRidge.xcodeproj -scheme QuackRidge -destination 'platform=macOS'
```

### Task 5: Implement the Swift supervisor, IPC client, and app state model

Connect the native shell to the bundled backend with strict ownership and state rules.

- [ ] Implement `BackendManifest` verification before launch, including architecture, hashes, versions, and extension inventory.
- [ ] Implement `ServiceSupervisor` with lifecycle pipe, private-event-channel readiness decoding, asynchronous raw stdout/stderr discard drains, bounded restart policy, and graceful shutdown.
- [ ] Construct a minimal reviewed environment for every daemon/doctor spawn instead of inheriting the parent; derive home/user/temp through trusted OS account APIs, use fixed/validated locale values and absolute paths, and exclude parent `HOME`/`TMPDIR`, credential-like, `QUACKRIDGE_*`, `DYLD_*`, and `PATH` values.
- [ ] Accept trusted lifecycle/log/result frames only from the one-client child-PID-verified event channel; continuously drain but never parse, display, hash, or export raw stdout/stderr payloads.
- [ ] Implement an actor-backed POSIX Unix-socket `ManagementClient` with strict frame limits, request IDs, deadlines, cancellation, and typed decoding.
- [ ] Implement exact handshake and externally managed adoption behavior without terminating unknown processes.
- [ ] Implement `AppModel` as the sole main-actor state source, with visibility-aware polling and immediate post-action refresh.
- [ ] Key pairing UI state to `(daemon_instance_id, pairing_generation)`; on any readiness/handshake/status generation change, cancel stale pairing work and replace prior success with a re-pair-required state before presenting paired health.
- [ ] Prevent overlapping mutations, discard out-of-order polling responses, and surface stale configuration conflicts without silent retries.
- [ ] Add dependency protocols and deterministic fakes for supervisor, client, clock, login item, and logs.
- [ ] Verify no sensitive payload is sent to unified logging, crash breadcrumbs, debug descriptions, or SwiftUI previews.

Validation commands after this task:

```sh
xcodebuild test -project macos/QuackRidge.xcodeproj -scheme QuackRidge -destination 'platform=macOS' -only-testing:QuackRidgeTests
make macos-backend-smoke
```

### Task 6: Build onboarding, menu-bar controls, and Overview

Deliver the complete first-run and daily monitoring experience against live state.

- [ ] Implement onboarding copy for the local-only bridge, Keychain boundary, read-only expectation, and relationship to PondPilot.
- [ ] Offer Launch at Login without enabling it by default, start the backend, and route the final onboarding action to Add Source or Pairing.
- [ ] Build the menu-bar popover with lifecycle, endpoint, uptime, source counts, highest-priority warning, and contextual actions.
- [ ] Build Overview with service health, source summary, PondPilot connection guidance, versions, and progressive technical disclosure.
- [ ] Make closing the window leave the menu-bar app active and make Quit describe and perform owned-daemon shutdown.
- [ ] Display externally managed mode accurately and avoid offering ownership-only controls.
- [ ] Verify every state has one clear primary action and no dead-end screen.

Validation commands after this task:

```sh
xcodebuild test -project macos/QuackRidge.xcodeproj -scheme QuackRidge -destination 'platform=macOS' -only-testing:QuackRidgeTests/OverviewTests
xcodebuild test -project macos/QuackRidge.xcodeproj -scheme QuackRidgeUITests -destination 'platform=macOS'
```

### Task 7: Build complete source management

Replace everyday `source` CLI usage with guided native workflows for every supported connector.

- [ ] Build the searchable source list, status filtering, enabled toggle, detail inspector, empty state, and degraded-source recovery actions.
- [ ] Build a shared wizard shell with connector selection, field validation, secure credential entry, test progress, result summary, and final review.
- [ ] Implement PostgreSQL fields: ID, name, alias, host, port, database, user, SSL mode, and managed root-certificate selection/import with current-reference display.
- [ ] Implement MySQL/MariaDB fields: ID, name, alias, host, port, database, user, and SSL mode.
- [ ] Implement SQLite and DuckDB file selection with `NSOpenPanel`, absolute-path validation, read-only explanation, and missing-file recovery.
- [ ] Implement ODBC DSN/driver selection, semantic database type, username/password, allowlisted public properties, and Keychain-backed secure custom properties without exposing values after submission.
- [ ] Implement edit semantics for keeping/replacing/removing credentials, retesting changed fields, alias-change warnings, revision conflicts, and unsaved changes.
- [ ] Implement removal confirmation that names both the source config and Keychain credential consequences.
- [ ] Clear credential and draft state after success, cancellation, window close, timeout, and backgrounding where practical.
- [ ] Verify source mutations update runtime health without requiring a manual restart.

Validation commands after this task:

```sh
go test ./internal/config/... ./internal/source/... ./internal/control/...
xcodebuild test -project macos/QuackRidge.xcodeproj -scheme QuackRidge -destination 'platform=macOS' -only-testing:QuackRidgeTests/SourceManagementTests
make macos-source-e2e
```

### Task 8: Build pairing and token management

Make PondPilot connection a first-class guided flow without exposing the long-lived token in normal use.

- [ ] Request one-time pairing challenges through management IPC with the production PondPilot origin allowlist, bounded TTL, and opaque pairing ID.
- [ ] Add daemon-side pairing status and cancellation so Swift can display waiting, consumed, expired, cancelled, and failed states without receiving the Quack token.
- [ ] Bind waiting and recently consumed pairing UI state to `(daemon_instance_id, pairing_generation)`; after any owned/external restart or in-app/out-of-band token rotation, invalidate the old outcome and offer one-click re-pairing.
- [ ] Display the challenge, expiration, copy action, Open PondPilot action, expired state, retry, and confirmed success state.
- [ ] Keep a challenge alive when the user navigates away, closes the manager, or switches to PondPilot; cancel it only on explicit Cancel, TTL expiry, successful consumption, owned-daemon shutdown, or app quit.
- [ ] Keep the Quack token inside Go for the normal flow and ensure Swift response models cannot decode it accidentally.
- [ ] Implement guarded manual recovery as a separate advanced action with explicit disclosure, short display lifetime, and clipboard clearing where reliable.
- [ ] Implement token rotation with confirmation that existing PondPilot connections will disconnect and must pair again; atomically generate and return a new `pairing_generation` with the new token.
- [ ] Serialize rotation with pairing creation: reject new challenges while rotating, close and await every outstanding pairing server, then rotate the token so no old-token response remains in flight; tell the user pending challenges were cancelled.
- [ ] Implement separate `manual_reveal_prepare` and `manual_reveal_consume` operations with a single-use thirty-second nonce, exact confirmation, isolated sensitive response type, expiry, replay protection, and no secret on failure.
- [ ] Require device-owner authentication before consume in Swift; keep the result only in the manual-recovery view model, conceal and clear it on timeout or close, and conditionally clear only the unchanged copied pasteboard value on timer, view close, and every normal Quit path.
- [ ] Test origin rejection, expiry, replay, multiple simultaneous attempts, rotation, and daemon restart, including successful pair → crash → automatic restart and out-of-band CLI rotation while the app is attached, with stale-success invalidation before paired health is shown.
- [ ] Ensure the long-lived token never enters normal pairing logs, notifications, accessibility announcements, app-generated captures, or support exports; use synthetic short-lived challenges in UI capture tests.

Validation commands after this task:

```sh
go test ./internal/pairing/... ./internal/control/...
go test -race ./internal/pairing/... ./internal/control/...
xcodebuild test -project macos/QuackRidge.xcodeproj -scheme QuackRidge -destination 'platform=macOS' -only-testing:QuackRidgeTests/PairingTests
```

### Task 9: Build diagnostics, logs, and settings

Complete the operational workflow so Terminal is unnecessary for routine recovery.

- [ ] Present diagnostics as version, bundle integrity, service, Keychain, source, network, and security-posture groups with severity and recovery actions.
- [ ] Invoke offline doctor only when management IPC cannot become available, using the reviewed minimal environment and a bounded child-PID-verified result channel; never decode raw stdout/stderr as doctor output.
- [ ] Implement structured log ingestion, strict redaction, file permissions, rotation, filtering, reveal, and clear-log actions.
- [ ] Drain raw stdout/stderr with a strict 64 KiB per-record framing cap solely to bound memory; regardless of shape, stream-discard records through delimiter/EOF without JSON decoding, retention, hashing, or payload-derived metadata, keeping only a rate-limited count plus fixed warning.
- [ ] Implement a previewable sanitized support report with explicit save/export and no automatic upload.
- [ ] Integrate `SMAppService.mainApp`, reflect actual registration state, and guide users through required System Settings approval.
- [ ] Add app/backend/protocol/DuckDB/extension version details, licenses, configuration and log locations, and copy-safe diagnostics.
- [ ] Add a Help action that opens the stable releases page without fetching or interpreting an update manifest in-app.
- [ ] Place token rotation and manual recovery under an Advanced section with confirmation and consequences.

Validation commands after this task:

```sh
xcodebuild test -project macos/QuackRidge.xcodeproj -scheme QuackRidge -destination 'platform=macOS' -only-testing:QuackRidgeTests/DiagnosticsTests
xcodebuild test -project macos/QuackRidge.xcodeproj -scheme QuackRidge -destination 'platform=macOS' -only-testing:QuackRidgeTests/SettingsTests
make macos-privacy-audit
```

### Task 10: Complete accessibility, interaction polish, and resilience

Treat native quality and recovery behavior as release requirements rather than post-release cleanup.

- [ ] Provide complete keyboard navigation, default/cancel actions, focus restoration, menu commands, and non-pointer alternatives.
- [ ] Verify VoiceOver labels, values, grouping, announcements, secure-field behavior, and error association for every workflow.
- [ ] Verify light/dark appearance, increased contrast, Reduce Transparency, Reduce Motion, larger text, and narrow window sizes.
- [ ] Use icon plus text or shape for every health state; meet contrast requirements without weakening macOS materials.
- [ ] Preserve useful state through window closure and app relaunch while never persisting credentials, tokens, or half-submitted destructive actions.
- [ ] Handle sleep/wake, network changes, Keychain lock/unlock, rapid open/close, daemon crash during mutation, and app termination during shutdown.
- [ ] Review all copy for concise explanation, specific recovery, consistent terminology, and no leakage of raw backend errors.
- [ ] Perform a manual human test from first install through source creation, pairing, degraded recovery, and uninstall.

Validation commands after this task:

```sh
xcodebuild test -project macos/QuackRidge.xcodeproj -scheme QuackRidge -destination 'platform=macOS'
xcodebuild test -project macos/QuackRidge.xcodeproj -scheme QuackRidgeUITests -destination 'platform=macOS'
make macos-accessibility-audit
```

### Task 11: Add native integration, UI, and security test coverage

Exercise the real app/backend boundary and prove cleanup and secret handling under failure.

- [ ] Add Swift unit tests for protocol decoding, app-state reduction, restart policy, polling races, field validation, log redaction, and support export.
- [ ] Add UI tests for onboarding, menu-bar access, window lifecycle, every connector form, pairing, diagnostics, settings, and accessibility identifiers.
- [ ] Add a test backend mode or fixture server only where deterministic UI failure injection is impossible with the real daemon; keep protocol fixtures identical to production.
- [ ] Add a real packaged-backend integration suite using disposable SQLite and DuckDB files for add, test, update, enable, reload, remove, pairing, and shutdown.
- [ ] Reuse disposable PostgreSQL/MySQL integration tests at the Go layer; never use a developer database or real Keychain in CI.
- [ ] Add deterministic startup/publication race tests covering CLI-before-daemon, daemon-before-CLI, and CLI waiting while a compatible or incompatible socket is published.
- [ ] Race last-release `serve` after legacy probe/before no-replace claim and while epoch flip is pending; prove exactly one side wins, migration aborts if the socket wins, the sentinel persists if migration wins, and no live v1 daemon is unlinked or orphaned.
- [ ] Kill migration after temporary claim creation, after claim rename/directory sync, before and after recovery-state sync, during credential staging, and immediately before marker flip; on restart, prove a verified owned attempt deterministically resumes or rolls back to working v1 while foreign non-socket entries remain untouched.
- [ ] Add live-health tests for post-attach failure, bounded probes, stale-generation suppression, backoff, source-scoped reattachment, recovery, reload/removal races, and manual refresh.
- [ ] Add certificate tests for valid CA chains, invalid PEM, private-key rejection, path/reference traversal, atomic import, reference-safe deletion, factory resolution, and migration.
- [ ] Add ODBC fixtures proving API key, access key, client secret, authentication blob, bearer, and unknown vendor properties never enter config or logs and are merged only from Keychain memory.
- [ ] Run migration fixtures from the last released config/CLI for public and sensitive ODBC properties, locked Keychain, staged-write failure, interruption before and after config flip, roll-forward recovery, and legacy-backup removal.
- [ ] Explicitly point the last released CLI binary at the v2 document and prove every mutation fails closed on the bumped format; invoke its default `serve` after activation and prove the non-socket legacy-control sentinel prevents a competing daemon.
- [ ] Pause the last released CLI after it loads v1 but before save/remove, activate the new state epoch, resume the late config write and legacy credential deletion, and prove the app restores the retirement document while the control sentinel, active marker, versioned credentials, and v2 config remain unchanged and secret-free across restart.
- [ ] Test the supported transition matrix: last-release Homebrew CLI install → epoch-aware `quackridge` upgrade → `quackridge-app` install → app migration → standalone source/status/offline/serve operations against active v2 state.
- [ ] Create and replace credentials in the signed app helper, then use the separately installed signed CLI artifact to read them in offline and `serve` modes; cover prompt approval, prompt denial, locked Keychain, and non-interactive failure without deletion or hangs.
- [ ] Add rotation/pairing race tests proving waiting challenges are cancelled and no pairing response remains active when token rotation completes.
- [ ] Add app-state and UI tests proving a changed `daemon_instance_id` or `pairing_generation` invalidates a consumed pairing outcome, never reports a restarted or externally rotated daemon as still paired, and presents a working re-pair action.
- [ ] Feed seeded low-entropy markers through raw stdout/stderr lines that exactly mimic every valid under-limit log/event shape, multi-chunk over-limit JSON/non-JSON, and never-terminated input; prove only genuine private-channel Go frames reach logs and memory/support exports retain no raw payload-derived value beyond one fixed count event.
- [ ] Seed the parent process with password/token, `QUACKRIDGE_*`, `DYLD_*`, fake `PATH`, and malformed locale variables; inspect daemon and doctor environments and support artifacts to prove only the reviewed validated allowlist reaches either child.
- [ ] Add Go/Swift fixtures and UI tests for guarded reveal preparation, local-auth denial, wrong confirmation, consume, replay, expiry, cancel, auto-hide, memory clearing, accessibility exclusion, support-export isolation, and pasteboard change-count/content safety across timer, view close, and normal Quit; verify newer clipboard data survives and crash/force-quit limitations are disclosed.
- [ ] Test malformed and oversized IPC, wrong protocol, wrong product, stale socket, non-socket collision, permission denial, slow operations, and daemon exit mid-request.
- [ ] Test adaptive polling rates, timer suspension across sleep, immediate wake/action refresh, and bounded idle socket activity.
- [ ] Test that config, logs, process listings, crash reports, app-generated captures outside the intentional manual-reveal fixture, support exports, clipboard cleanup, and build artifacts contain no seeded secret markers; separately verify the reveal warning accurately states the unavoidable system/user capture limitation.
- [ ] Verify teardown leaves no daemon, socket, pairing listener, temporary database, Keychain fixture, login item, mounted DMG, or test log directory.
- [ ] Run Thread Sanitizer where practical for Swift actors and the Go race detector for management concurrency.

Validation commands after this task:

```sh
make check
make integration
make macos-test
make macos-e2e
make macos-privacy-audit
```

### Task 12: Package, notarize, publish, and document the app

Extend the release pipeline without regressing existing CLI artifacts or Linux support.

- [ ] Build Release apps natively on macOS ARM64 and AMD64 with exact matching backend binaries and extension sets.
- [ ] Reuse the exact signed per-architecture helper artifact in the corresponding standalone macOS CLI archive, verify the same signing identifier/team/entitlements after extraction, and stop publishing unsigned replacement CLI binaries.
- [ ] Sign every nested executable or loadable extension first with the approved entitlements and verify its Hardened Runtime, secure timestamp, stable identifier, and DuckDB compatibility before calculating any bundle digest.
- [ ] Generate `backend-manifest.json` from the final signed nested bytes, add immutable licenses and privacy metadata, verify the recorded helper/extension hashes, and only then sign the outer app so its resource seal covers the final manifest.
- [ ] Notarize and staple the signed app, package that exact app into the DMG, then notarize and staple the final DMG; retain the notary logs and reject warnings that weaken the release claim.
- [ ] Generate the externally distributed component SBOM and provenance after the app is signed, using final build identities and nested digests; do not embed an artifact-hash file that would invalidate the app seal. Compute release checksums and signatures only from the final stapled DMGs.
- [ ] Mount, install, Gatekeeper-assess, launch, manage a disposable source, pair, quit, and uninstall from the exact final DMG in a clean temporary user context on each architecture.
- [ ] From each exact mounted/installed DMG, run the approved unixODBC/PostgreSQL/MySQL external-driver matrix under Hardened Runtime; gate or explicitly defer ODBC if either architecture cannot load and query safely.
- [ ] Run the same exact-DMG launch, login-item, Keychain, source, pairing, and shutdown smoke on macOS 13 ARM64 and Intel using clean VMs or dedicated hosts; record manual evidence only when automated infrastructure is unavailable.
- [ ] Preserve the strict release-manifest v2 schema and stable feed as CLI-only for already distributed consumers; do not add `kind` or any other field in place.
- [ ] Publish a separately versioned release-manifest v3 schema and feed with an explicit asset kind so app DMGs and CLI archives can coexist for the same platform/architecture without ambiguous selection; its Quack protocol compatibility range remains independent of manifest schema version.
- [ ] Add app and CLI assets to the v3 manifest with kind, platform, architecture, minimum OS, version, hashes, signatures, and download URLs while continuing to generate a valid CLI-only v2 manifest from the same release inputs.
- [ ] Preserve the existing Homebrew cask token `quackridge` and its `binary` installation as the headless CLI compatibility surface; publish the GUI under a distinct `quackridge-app` cask that installs `QuackRidge.app` and does not claim or replace the `quackridge` command.
- [ ] Make first launch refuse epoch activation when the Homebrew receipt or standard ARM64/Intel install locations identify a standalone CLI that lacks state-epoch support, with exact `brew upgrade --cask quackridge` and archive-upgrade guidance; absence of a standalone CLI is allowed.
- [ ] Add a Homebrew transition/coexistence test starting from the last released `quackridge` cask, upgrading it to the epoch-aware signed CLI before installing `quackridge-app`, and proving `command -v quackridge`, active-state source/status/offline/serve behavior, and representative scripts remain intact.
- [ ] Block app publication unless native smoke, management E2E, privacy audit, signing verification, notarization, and Gatekeeper assessment all pass.
- [ ] Document installation, first launch, Launch at Login, source workflows, pairing, diagnostics, logs, CLI coexistence, migration from archive installs, the absence of in-app update checking, and complete uninstall.
- [ ] Update security and troubleshooting docs for process ownership, entitlements, local IPC, support export, Keychain prompts, and externally managed mode.
- [ ] Publish a prerelease first, complete a manual acceptance pass on both architectures, then promote only the exact tested artifacts.

Validation commands after this task:

```sh
make macos-release VERSION=<version> ARCH=arm64
make macos-release VERSION=<version> ARCH=amd64
./scripts/verify-macos-dmg.sh dist/quackridge_<version>_darwin_arm64.dmg
./scripts/verify-macos-dmg.sh dist/quackridge_<version>_darwin_amd64.dmg
./scripts/verify-release-assets.sh dist/
```

`verify-macos-dmg.sh` must validate the staple on the DMG, mount it read-only at a fresh path, validate the contained app's staple and nested signatures, recompute every `backend-manifest.json` digest from the post-signing mounted bytes, run Gatekeeper assessment against both distribution and executable contexts, copy the app to a clean Applications directory, launch and exercise the packaged backend, and detach the image even when a check fails. It must never substitute an app from the build directory for the app mounted from the final DMG.

## Full validation matrix

Run the complete matrix before promoting a macOS app release:

```sh
test -z "$(gofmt -l .)"
go vet ./...
go test ./...
go test -race ./...
govulncheck ./...
go test -tags=integration ./...
go test -tags=e2e ./...
go build ./cmd/quackridge

xcodebuild build -project macos/QuackRidge.xcodeproj -scheme QuackRidge -configuration Release -destination 'platform=macOS'
xcodebuild test -project macos/QuackRidge.xcodeproj -scheme QuackRidge -destination 'platform=macOS'
xcodebuild test -project macos/QuackRidge.xcodeproj -scheme QuackRidgeUITests -destination 'platform=macOS'

make macos-packaging-spike
make macos-odbc-packaging-spike
make macos-backend-smoke
make macos-source-e2e
make macos-e2e
make macos-accessibility-audit
make macos-privacy-audit
./scripts/verify-macos-dmg.sh dist/quackridge_<version>_darwin_arm64.dmg
./scripts/verify-macos-dmg.sh dist/quackridge_<version>_darwin_amd64.dmg
./scripts/verify-release-assets.sh dist/
```

Native CI must run the architecture-specific packaged artifact on `macos-15` ARM64 and `macos-15-intel` AMD64. The release gate additionally runs the exact final DMGs on clean macOS 13 ARM64 and Intel systems. Use automated clean VMs or dedicated hosts where available; until that infrastructure exists, attach recorded manual evidence for both minimum-OS/architecture combinations to every release candidate. Tests that require Developer ID credentials or notarization run only in protected release jobs; pull requests still perform ad-hoc signing and bundle-integrity smoke tests.

## Release gates

The app cannot be published until all of the following are recorded in the release checklist:

- The Hardened Runtime spike passed for bundled DuckDB extensions and the exact signed/notarized helper loaded unixODBC plus approved PostgreSQL/MySQL drivers on ARM64 and Intel; otherwise ODBC is explicitly deferred from the release.
- The exact management protocol fixtures pass in both Go and Swift.
- All source mutations demonstrate cross-process exclusion plus config, versioned-Keychain, journal, and runtime recovery under injected failures and forced process termination at every transaction phase.
- The atomic committed-head pointer selects only the candidate primary/recovery snapshot after commit, so primary corruption cannot resurrect a deleted credential reference; multiple durable cleanup-debt records survive crashes and later mutations before bounded drain succeeds.
- Startup/offline-mutation race tests prove configuration cannot change between daemon bootstrap and control publication without being observed.
- App-owned startup times out and reaches a recoverable failed state at every deliberately hung phase.
- Live health degradation, bounded retry, source-scoped reattachment, and recovery update status without a manual daemon restart.
- A successful pairing followed by a crash/restart or an out-of-band token rotation invalidates the old `(daemon_instance_id, pairing_generation)` success before paired health is shown and gives the user a direct re-pair action.
- PostgreSQL CA references resolve only through the managed certificate store, and ODBC credential-class or unknown properties never enter plain configuration.
- The v1-to-v2 config migration moves legacy sensitive ODBC properties into versioned Keychain credentials or fails without changing the authoritative v1 file; a last-released binary rejects an explicitly supplied v2 fixture, while default-path writes from a legacy process are confined to retired v1 state.
- A paused last-released CLI cannot overwrite or become authoritative after the active state epoch switches to v2.
- An epoch-aware standalone CLI is installed before app activation when one is present; no-replace legacy-control claim races prove either old `serve` wins and migration aborts untouched or migration wins and the persistent sentinel prevents a competing daemon.
- Kill recovery at every pre-flip claim boundary proves a self-identifying owned claim resumes or restores working v1 deterministically, while foreign non-socket entries are never removed.
- A denied superseded-Keychain-item deletion after commit leaves the new config/runtime healthy, records only non-secret cleanup debt, and succeeds on a later retry without rollback.
- Guarded manual reveal succeeds only through its isolated, authenticated, single-use contract and leaves no token in logs, accessibility output, app-generated capture/support artifacts, clipboard after the timer, view close, or normal Quit, or support exports; newer clipboard contents survive, and disclosure accurately warns that crash/force-quit cleanup and system/user capture cannot be guaranteed.
- The exact packaged backend starts, serves identity, adds a disposable file source, pairs, reloads, and stops on both architectures.
- A secret-seeded parent environment is reduced to the reviewed child allowlist for daemon and doctor. No seeded marker appears in config, arguments, child environments, trusted event frames/logs, app-generated captures outside the deliberate reveal fixture, reports, SBOMs, or release assets; valid-schema spoof, oversized, and unterminated raw stdout/stderr remain memory-bounded and payload-free.
- VoiceOver and keyboard-only acceptance passes are recorded for onboarding, source add/edit/remove, pairing, diagnostics, and quit.
- Codesign verification, app and DMG notarization/stapling, distribution and executable Gatekeeper assessment, and clean-user installation pass from the exact final DMGs.
- Mounted app verification proves every backend-manifest digest matches the final post-signing nested bytes.
- Exact-DMG runtime acceptance passes on macOS 13 and macOS 15 for both ARM64 and Intel.
- Signed-artifact Keychain tests pass in both directions: app helper reading prior-CLI credentials and standalone signed CLI reading app-created/replaced credentials offline and in `serve`, with bounded prompt/denial/headless recovery and no deletion of inaccessible items.
- Existing CLI archives, Linux CI, protocol compatibility, and PondPilot pairing tests remain green.
- Upgrading the last released Homebrew `quackridge` cask to the epoch-aware CLI before installing `quackridge-app` preserves the executable, routes it to active v2 state, and keeps representative automation working.
- Existing strict release-manifest v2 consumers continue receiving a valid CLI-only feed, while v3 compatibility tests select app and CLI assets by explicit kind.
- Documentation accurately describes experimental limitations, especially `cancellation_noop`, read-only posture, external ODBC requirements, the lack of in-app update checking, and uninstall.

## Migration and compatibility

- Import the legacy `~/Library/Application Support/QuackRidge/config.json` once, then make a versioned state directory such as `state-v2/` authoritative for config, lock, active journal, cleanup-debt queue, committed head/recovery snapshots, certificates, and control socket. Preserve Keychain service `io.pondpilot.quackridge` so current users retain credentials.
- The app migrates config only through the Go backend's atomic migration machinery. Swift never opens the config for mutation, and no legacy secret-bearing backup remains after a successful epoch switch.
- The new backend bumps the persisted config format before its first mutation. A previously released CLI explicitly pointed at a v2 document must reject it as unsupported. Before migration reads or stages state, the app probes, prewrites/fsyncs a self-identifying claim, and atomically renames it no-replace/no-follow onto the vacant legacy control path. With no active marker, restart uses verified claim/migration identity to resume or roll back; ordinary pre-flip failure removes only the exact owned claim, while activation retains it as sentinel and replaces the old config with a minimal non-secret retirement document.
- An atomic active-epoch marker selects the new state root. After activation, new binaries never read a recreated legacy config; if a previously loaded command writes it late, the app restores the known retirement document without inspecting or importing the stale contents. The control sentinel remains in place, so a fresh old `serve` cannot create a competing daemon.
- Legacy ODBC migration runs before service bootstrap under the startup lock. It either publishes a fully sanitized new-format config backed by versioned Keychain values or leaves the old-format config authoritative and returns a recoverable error; no mixed format is accepted.
- A CLI installed elsewhere sends mutations through the app-owned daemon when its management protocol is compatible. An incompatible live daemon makes the CLI refuse mutation. Offline mutation uses the shared advisory lock, and revision revalidation under that lock prevents silent lost updates.
- Before activation, the Go migration preflight checks the Homebrew receipt and standard ARM64/Intel binary locations for state-epoch capability and blocks with upgrade guidance if the installed CLI is too old. The supported Homebrew sequence upgrades `quackridge` first and installs the separate `quackridge-app` cask second; arbitrary copied legacy binaries remain unsupported and isolated by the retirement/control sentinels.
- Migration clones every accessible fixed Keychain credential into an immutable versioned reference before activation, so a late legacy remove cannot break v2. Failed access to a legacy item blocks the all-or-nothing epoch switch and offers unlock/retry or secure re-entry without deleting the legacy item.
- New standalone macOS CLI archives contain the exact signed helper artifact used by the matching app build, preserving its stable code requirement and reviewed Keychain access policy. Bidirectional signed-artifact tests, not service/account-name equality alone, are the compatibility gate.
- The bundled CLI remains callable directly for support, but documentation should recommend the standalone CLI or a small launcher command rather than encouraging users to execute a path inside the app bundle.
- Mac app and backend versions are released together. Mixing a GUI from one release with a backend extracted from another is unsupported and fails during handshake or manifest verification.
- Existing macOS CLI tarballs remain available during at least the app's prerelease period to avoid breaking automation and Homebrew users.
- The existing Homebrew token `quackridge` remains the epoch-aware CLI package and keeps installing the `quackridge` binary. The GUI uses the distinct `quackridge-app` cask and may coexist with it; any future consolidation requires a separately reviewed Homebrew rename/migration release.
- The existing strict release-manifest v2 URL and schema remain CLI-only. App-aware consumers opt into the separately versioned v3 URL; manifest schema version and browser-facing Quack protocol version are negotiated independently.

## Principal risks and mitigations

| Risk | Mitigation |
| --- | --- |
| Hardened Runtime blocks DuckDB extensions | Complete Task 0 first; isolate any library-validation exception to the backend; keep digest and DuckDB signature checks. |
| Hardened Runtime blocks external ODBC driver dylibs | Test unixODBC and representative PostgreSQL/MySQL drivers from exact signed/notarized artifacts on both architectures; use a helper-only minimal exception or defer ODBC. |
| Config/Keychain/runtime divergence after failure or crash | Immutable credential references, a synced non-secret journal, deterministic startup recovery, and phase-by-phase process-kill tests. |
| Post-commit Keychain cleanup is denied | Keep candidate state authoritative, retain only non-secret cleanup debt, warn without blocking service, and retry deletion later. |
| Multiple cleanup failures overwrite the active journal or block later mutations | Atomically rename each committed journal into a unique debt record, sync the queue, free the active slot, and drain only bounded work under the lock. |
| Config fallback resurrects a deleted credential reference | Atomically point committed head at the immutable candidate snapshot before cleanup; reject legacy `.bak`, directory-order selection, and malformed head metadata. |
| GUI and CLI race | Route live CLI mutations through the daemon, use a shared advisory lock online and offline, and recheck revision under the lock. |
| Daemon startup races offline CLI fallback | Hold the shared lock through control publication and require a second socket probe after offline CLI lock acquisition. |
| Startup hangs on source or Keychain work | Apply a cancellable sixty-second deadline, terminate only the owned child, run offline diagnostics, and enter a retryable failed state. |
| Source status becomes stale after attachment | Run bounded daemon-side health probes and source-scoped reattachment with generation checks and backoff. |
| Restart or out-of-band rotation leaves a stale successful-pairing indication | Carry daemon and pairing generations through readiness/handshake/status/rotation, key pairing outcomes to both, and force a re-pair-required UI state whenever either changes. |
| Certificate references are unusable or unsafe | Import immutable validated CA bundles into a managed store and resolve references only inside that store. |
| ODBC custom properties leak credentials | Persist only reviewed public keys and route every unknown or credential-class property through the versioned Keychain credential. |
| A previously released CLI bypasses new locking or races migration with a second daemon | Claim the legacy control path atomically before migration, abort if old `serve` wins, retain the claim as a sentinel, and make v2 a separate authoritative epoch. |
| Crash strands the pre-flip legacy-control claim and disables v1 | Use a fully written self-identifying claim plus synced migration identity; resume or remove only a verified owned attempt when no active marker exists. |
| An already-running legacy CLI saves cached v1 after migration | Switch authority to a new state epoch atomically and never read the retired path after activation; test a paused legacy late writer. |
| Legacy ODBC properties already contain secrets | Perform an all-or-nothing startup migration into versioned Keychain values without copying raw properties into the journal. |
| Signing invalidates precomputed backend hashes | Sign nested bytes first, generate the backend manifest second, verify hashes, then sign the outer app; keep SBOM/provenance external and hash only final DMGs. |
| Adding app assets breaks strict v2 manifest consumers | Keep v2 CLI-only and publish an explicitly versioned v3 schema/feed for typed app and CLI assets. |
| App packaging removes the Homebrew CLI used by scripts | Keep `quackridge` as the existing CLI cask, publish `quackridge-app` separately, and run an upgrade/coexistence test from the last release. |
| App-created Keychain items are unreadable from the standalone CLI | Reuse the exact signed helper artifact and access policy in both packages, then test app-to-CLI access offline and in `serve` with prompt/denial/headless cases. |
| Native driver stderr spoofs a valid structured log containing a secret | Accept structured events only on a child-PID-verified private channel and always stream-discard raw stdout/stderr regardless of shape. |
| Oversized or unterminated stderr exhausts app memory | Cap retained bytes per record and stream-discard through delimiter/EOF while continuing to drain the pipe. |
| Helper inherits secret or loader-injection environment variables | Construct a minimal validated environment for daemon and doctor, use absolute paths, and test from a secret-seeded parent. |
| Manual recovery leaks the long-lived token into normal models | Use isolated prepare/consume operations, device-owner authentication, a single-use nonce, short-lived view state, and clipboard/export tests. |
| Normal Quit strands a copied token on the pasteboard | Apply the same change-count/content guard synchronously on view close and normal termination; disclose that crash/force quit cannot guarantee cleanup. |
| User assumes manual reveal cannot be screen-captured | Scope capture guarantees to app-generated artifacts and warn that macOS/user capture cannot be reliably prevented while the token is visible. |
| App crash leaves a daemon | Lifecycle pipe EOF causes owned-daemon shutdown; native cleanup tests verify it. |
| External daemon is mistaken for app-owned | Exact handshake plus in-memory process ownership; never signal a PID merely because a socket responds. |
| Secrets leak through Swift or diagnostics | Secret-free response types, short-lived form state, redaction tests, seeded-marker audits, and preview-before-export. |
| Restart loop drains resources | Two bounded retries, then explicit failed state and user-driven Retry. |
| Architecture packaging drifts | Architecture-specific DMGs, signed backend manifest, native artifact smoke on ARM64 and AMD64. |
| Native UI becomes a second engine implementation | Keep all source, config, Keychain, pairing, and runtime behavior in Go; Swift only presents typed operations. |
| App grows into a query client | Keep query execution and exploration in PondPilot; the native app remains an operations manager. |
| Minimum deployment target works only at compile time | Run the exact final DMGs on clean macOS 13 ARM64 and Intel systems before every release. |

## Definition of done

The work is complete when a user on a clean supported Mac can install the notarized DMG, opt into launch at login, add and test any supported source, observe live health, pair PondPilot, recover from a degraded source, export safe diagnostics, rotate the token with clear consequences, and uninstall completely without using Terminal—and when the exact same backend remains fully operable through the documented CLI with no security or compatibility regression.
