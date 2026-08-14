# QuackRidge for macOS

The native macOS app owns a single per-user QuackRidge backend and provides the
routine source, health, pairing, diagnostics, and Launch at Login workflows
without requiring Terminal. It requires macOS 13 or newer. The app is a menu-bar
application with a separate manager window; closing that window does not stop
the backend.

The GUI and backend are separate executables. The app verifies the bundled
helper and every DuckDB extension against `backend-manifest.json` before launch,
starts the helper with a minimal environment, and communicates over a private
mode-`0600` Unix socket using management protocol v2. Backend standard output and
standard error are drained and discarded. Credentials remain in Keychain and
are never returned over management IPC.

## Install and first launch

Release builds are distributed as `QuackRidge.app` in a DMG and may also be
installed by the separate `quackridge-app` Homebrew cask. Drag the app to
Applications, launch it, review the security model, and choose whether to start
at login. Launch at Login is opt-in and uses the macOS Login Items interface.
The app does not check for or install updates; use the documented release page
or Homebrew explicitly.

The app adopts a compatible backend already running for the same user instead
of starting a second one. The CLI likewise routes supported live operations to
the app-owned backend. An incompatible endpoint is left untouched and produces
an upgrade/recovery error.

## Sources and certificates

Use **Sources → Add Source** to test and save PostgreSQL, MySQL/MariaDB, SQLite,
DuckDB, or ODBC sources. Passwords and ODBC secure properties are submitted only
as credential material and stored under immutable, versioned Keychain
references. Configuration contains no credential values.

PostgreSQL custom CAs must be imported into QuackRidge's managed certificate
store. Configuration stores a content-addressed certificate reference, not the
selected path. ODBC still depends on a separately installed unixODBC driver
manager and a compatible vendor driver; these external drivers are not bundled.

## Pairing and recovery

The Pairing screen creates a loopback-only, origin-bound, single-use challenge
that expires after two minutes. Restarting the backend or rotating its token
invalidates prior pairing state. Do not place tokens in URLs, screenshots,
support reports, or shell history.

Diagnostics and exported support data contain versions, source IDs/types and
sanitized health information. They deliberately omit credentials, tokens, SQL,
results, source hosts, database file paths, DSNs, and raw child output. The
current Quack data plane advertises `cancellation_noop`: a client timeout does
not prove that native work stopped.

## Files and uninstall

The per-user state root is `~/Library/Application Support/QuackRidge/`. To
uninstall completely:

1. Disable Launch at Login in QuackRidge Settings.
2. Remove each source in the app so its credential is removed from Keychain.
3. Quit QuackRidge; the app shuts down only the backend process it owns.
4. Remove `QuackRidge.app` and its application-support directory.
5. Remove the QuackRidge connection in PondPilot.

No kernel extension, privileged helper, browser extension, or automatic updater
is installed.
