# Experimental release checklist

No release is supported merely because a tag or archive exists. Promotion
requires the GitHub `Native release` workflow to pass every gate below.

## Automated gates

- [ ] Formatting, vet, unit, race, vulnerability, integration, and E2E tests pass.
- [ ] macOS AMD64, macOS ARM64, and Linux AMD64 build natively.
- [ ] Each native job starts DuckDB/Quack 1.5.5 from the bundled extensions and completes the identity/shutdown smoke test.
- [ ] Each deterministic archive contains the binary, exact extension set, upstream checksums/URLs, license texts, notices, and an SPDX JSON SBOM.
- [ ] Every archive has a Sigstore bundle issued to this repository's release workflow.
- [ ] The exact assembled archive is extracted and starts successfully on its matching native runner before publication.
- [ ] `release-manifest.json` validates against protocol v2 and contains only the smoke-tested assets.
- [ ] The combined checksums and release manifest are keyless-signed and verified before upload.
- [ ] Secret scanning confirms no passwords, DSNs, tokens, SQL text, temporary credentials, private result data, or private paths appear in artifacts, logs, screenshots, or traces.
- [ ] PondPilot's pinned native browser suite passes pairing, metadata trees, previews, single- and multi-table queries, exports, reload/reconnect, denied SQL, pairing failures, token rotation, partial source failure, and the visible cancellation no-op state.

## Evidence to record

For each promoted release, add links to the release workflow, all three native
jobs, the PondPilot pinned E2E workflow, the scheduled newest-prerelease run,
the signed manifest, checksums, SBOMs, and provenance bundles. Record the exact
QuackRidge tag, PondPilot commit, DuckDB version, extension build IDs, and any
accepted deviation.

## Manual promotion gates

- [ ] Review generated release notes and Homebrew cask.
- [ ] Verify the stable PondPilot-controlled manifest URL serves the signed bytes from the promoted GitHub release.
- [ ] Publish `v0.1.0` only after every automated and manual gate has evidence.
- [ ] Pin that exact tag and protocol in PondPilot and keep the UI experimental.
- [ ] Update `docs/platform-support.md` from “pending” only for native jobs that passed.

The workflow deliberately cannot promote a release when any native build,
signature, manifest, exact-archive smoke, or verification job is missing.

## Local pre-release evidence — 2026-08-11

The implementation checkout passed `gofmt`, `go vet`, unit tests, race tests,
the `e2e` build tag, a trimmed production build, actionlint, protocol drift
checking against PondPilot, and `govulncheck` with no called vulnerabilities.
The pinned Linux AMD64 extensions passed their upstream compressed checksums,
DuckDB/Quack identity and clean shutdown passed against the real extension
bundle, `doctor` verified the extracted bundle, and two independently generated
archives were byte-identical. The release-asset privacy audit also passed.

After Docker became available, the full integration command passed against its
disposable PostgreSQL 17 fixture, including attachment, metadata, server-side
queries and joins, read-only policy enforcement, and cleanup. PondPilot's
dedicated browser suite passed its negative pairing-security scenario, while
the positive production-build query scenario stalled before the statement
reached QuackRidge; a later manual run against PondPilot's development server
completed successfully. The automated PondPilot browser gate, three native
runner jobs, signatures, stable manifest publication, and release promotion
remain intentionally unverified and unchecked above.

Windows AMD64 was removed from the v0.1 release matrix after its native runner
proved that `duckdb-go` requires `windows_amd64_mingw` extensions while the
pinned upstream bundle is available only for `windows_amd64` (MSVC). Publishing
an unstartable Windows archive would violate the native-smoke release gate.

## Signing identity and rotation

Releases use Sigstore keyless signing with GitHub Actions OIDC. There is no
long-lived private signing key to store or rotate. Verification pins both the
issuer (`https://token.actions.githubusercontent.com`) and this repository's
`release.yml` workflow identity. Changing the repository owner, workflow path,
or OIDC issuer is a signing-identity rotation: review it as a security-boundary
change, update verification instructions and consumers in the same release,
retain old bundles for historical verification, and record the old and new
certificate identity patterns in the release notes.
