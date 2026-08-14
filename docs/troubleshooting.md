# Troubleshooting

Start with `quackridge doctor --extensions /path/to/extensions`. Add `--json`
for support automation. An error check causes a non-zero exit; warnings report
optional live checks that could not run.

## Common failures

| Symptom | Meaning and recovery |
| --- | --- |
| Extension checksum failure | Restore the complete verified release archive. Never download individual executable extension files. |
| Unsupported version pair | The binary and extension directory came from different releases. Re-extract one complete archive. |
| Credential store unavailable | Unlock Keychain/Credential Manager/Secret Service and run the command as the same local user that added the source. |
| Source unavailable | Check DNS, loopback/firewall policy, PostgreSQL TLS trust, database/user names, and the dedicated role's `CONNECT` grant. |
| Read-only posture warning | Remove elevated role attributes and all non-`SELECT` table grants; set `default_transaction_read_only=on`. |
| Stale control socket | Confirm no QuackRidge process is running, then remove only the reported `control.sock`. A normal shutdown removes it automatically. |
| Pairing rejected | Generate a new challenge, use the exact one-time code before expiry, and pair from the allowed PondPilot origin. |
| PondPilot disconnects after rotation | Pair again. Token rotation intentionally invalidates existing attachments. |
| Query timed out but server remains busy | v0.1 advertises `cancellation_noop`; wait for limits to stop the work or restart QuackRidge after assessing other active queries. |
| App reports bundle verification failure | Replace the complete app from one verified release. Do not modify or replace its helper or bundled extensions. |
| App cannot start its backend | Quit other QuackRidge instances, inspect Diagnostics, and confirm the app can write its per-user Application Support directory. An incompatible live endpoint is never unlinked automatically. |
| Launch at Login needs approval | Open macOS System Settings → General → Login Items, approve QuackRidge, then reopen app Settings. |
| ODBC works in Terminal but not the app | Confirm the driver uses an absolute configured path and works without shell-only `PATH`, `DYLD_*`, or other environment variables. External ODBC drivers are not bundled. |

## Diagnostics and privacy

Diagnostics report product/protocol/extension versions, loopback endpoint,
source IDs and health, configuration permissions, credential availability, and
locked DuckDB resource settings. They do not return credential values, tokens,
DSNs, SQL text, filesystem contents, or query results.

Before sharing logs, confirm they contain no private hostnames or source display
names. Do not attach a configuration file, credential-store export, browser
trace, database result, or raw process environment to a public issue. Report
suspected vulnerabilities through the private GitHub security-advisory form.
