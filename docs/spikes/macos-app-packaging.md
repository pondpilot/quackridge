# macOS app packaging and Hardened Runtime spike

Date: 2026-08-14
Host: `mini1`, Apple Silicon, macOS 15.1.1 (24B91)

## Conclusion

**Task 0 is FAIL/BLOCKED. Do not begin the production app implementation on
the strength of this host's results.**

The exact pinned ARM64 backend and extension set pass the existing native
identity/control-plane smoke before Hardened Runtime. They do not load from the
signed app with the required entitlement boundary. An explicitly temporary
diagnostic signature made the extension set load only after
`com.apple.security.cs.disable-library-validation` was also added to the outer
GUI app. The approved plan forbids that entitlement on the GUI executable.

That diagnostic used ad-hoc signatures and is not evidence that a Developer ID
artifact will behave identically. This host has no Developer ID identity or
notarization credentials, so the required production experiment cannot be
completed here and no architectural conclusion should be promoted from the
ad-hoc result alone. The gate remains closed until the exact Developer ID,
notarized, stapled, mounted-DMG experiment passes with the exception absent or
confined to the helper.

ODBC is independently blocked: unixODBC and representative PostgreSQL/MySQL
drivers are not installed, and no native Intel test host or disposable driver
endpoints are available. ODBC must be deferred unless its exact-DMG matrix later
passes on both architectures.

## Reproducible gate

The spike added these production-failing-by-default entry points:

```sh
make macos-packaging-spike
make macos-odbc-packaging-spike
```

`macos-packaging-spike` requires a native architecture, an installed Developer
ID Application identity in `MACOS_SIGNING_IDENTITY`, and a notarytool Keychain
profile named by `MACOS_NOTARY_PROFILE`. It builds with a fresh task-local Go
cache and `MACOSX_DEPLOYMENT_TARGET=13.0`, verifies the extension digests before
and after signing, signs nested code before the app, notarizes and staples the
app and DMG, mounts the final DMG read-only, and repeats signature, Gatekeeper,
doctor, and engine identity/control-plane checks only against mounted bytes.

For local diagnosis only, `ALLOW_ADHOC=1` permits an explicitly partial run.
`ALLOW_LIBRARY_VALIDATION_EXCEPTION=1` selects the candidate helper-only
exception. A partial run deliberately exits nonzero and never claims
notarization, Gatekeeper, or final-DMG success.

`macos-odbc-packaging-spike` accepts only the fixed Task 0 DMG path, verifies
the digest emitted by that gate, mounts it read-only, and starts its packaged
helper with PostgreSQL and MySQL DSNs. DSN
names are supplied through the non-secret `QUACKRIDGE_ODBC_POSTGRES_DSN` and
`QUACKRIDGE_ODBC_MYSQL_DSN` inputs. Successful startup performs the connector's
read-only metadata query through the real unixODBC driver. The gate must be run
with reviewed disposable databases and native driver installations.

## Evidence recorded on mini1

### Inputs and architectures

- Pinned DuckDB 1.5.5 ARM64 and AMD64 extension archives downloaded from the
  existing repository URLs and matched every checked-in compressed checksum.
- All six decompressed extensions identify as the expected Mach-O architecture.
- Production-style CGO Go helpers cross-built for ARM64 and AMD64.
- `otool -l` reports `LC_BUILD_VERSION minos 13.0` for both helpers.
- The load commands reference only `libresolv`, Security, CoreFoundation,
  `libSystem`, and `libc++` at stable system paths.
- The AMD64 binary could not complete a useful translated runtime smoke on this
  ARM64 host. The plan requires a native Intel job, so the cross-build result is
  build evidence only.

### Extension integrity and paths

- Unsigned/native ARM64 `doctor` passed from
  `QuackRidge Gate ü.app/Contents/Helpers/quackridge`.
- The existing release identity/control-plane smoke passed from the same app
  path before Hardened Runtime.
- Outer-app signing did not change any checked-in decompressed extension digest;
  every `extensions.sha256` check remained green.
- The upstream extension Mach-O files carry ad-hoc signatures and no Team ID.
  The spike does not replace those signatures.

### Hardened Runtime entitlement experiment

Selected intended entitlements:

- GUI app: empty entitlement set.
- Backend first attempt: empty entitlement set.
- Backend second attempt: only
  `com.apple.security.cs.disable-library-validation = true`.

Observed results:

1. With Hardened Runtime and no exception, signatures and `doctor` digest checks
   passed, but engine startup returned `QR_INTERNAL: load required extension
   failed`.
2. With the exception on the backend only, the same engine startup failure
   remained.
3. The byte-identical signed helper passed when copied outside the signed app.
4. As a diagnostic only, re-signing the outer app with the same exception made
   the helper load the extensions and pass the release smoke.

Step 4 violates the approved entitlement boundary. That artifact was neither
accepted nor notarized and must not be shipped.

### Signing, notarization, ODBC, and Keychain prerequisites

- `security find-identity -v -p codesigning`: `0 valid identities found`.
- `xcrun notarytool history`: credentials required; no usable profile supplied.
- `xcodebuild -version`: unavailable because only Command Line Tools are
  installed. The installed Swift compiler and SDK also reject a minimal
  Foundation build because their patch versions differ.
- `odbcinst`: not installed; no unixODBC driver registry or representative
  PostgreSQL/MySQL driver dylibs were found.
- No previously distributed `quackridge` executable is installed on `PATH`, so
  bidirectional signed-artifact Keychain compatibility cannot be exercised.
- Login Keychain settings were not accessible from this execution context.
  Prompt, denial, locked-Keychain, and non-interactive behavior therefore remain
  unproved.

## Signing order and bundle layout

The retained gate implements this order:

1. Fetch and verify the untouched upstream extension bytes.
2. Build the architecture-matching helper and minimal host for macOS 13.
3. Recheck extension digests.
4. Sign the helper with identifier
   `io.pondpilot.quackridge.backend` and the selected helper entitlements.
5. Sign the outer app with identifier `io.pondpilot.quackridge` and no
   library-validation exception.
6. Recheck strict nested signatures and extension digests.
7. Run doctor and engine smoke from the signed app.
8. For a Developer ID run only: notarize/staple the app, place that exact app in
   a signed/notarized/stapled DMG, mount it read-only, and rerun every check from
   the mounted copy.

The disposable bundle is:

```text
QuackRidge Gate ü.app/
  Contents/
    MacOS/QuackRidge
    Helpers/quackridge
    Resources/Backend/extensions/*
    Info.plist
```

## Requirements to reopen Task 0

1. Install a matching full Xcode and select it with `xcode-select`.
2. Install the proposed Developer ID Application certificate/private key and
   provide a notarytool Keychain profile.
3. Run the exact gate natively on ARM64 and Intel.
4. Provide reviewed unixODBC plus representative PostgreSQL and MySQL drivers,
   with disposable read-only test databases, on both architectures.
5. Provide the previously distributed signed CLI artifact and isolated
   Keychain fixtures for both-direction compatibility and denial/lock tests.
6. If the Developer ID experiment still requires the exception on the GUI,
   stop and approve a revised helper/extension signing architecture; do not
   continue with the current wrapper design.
