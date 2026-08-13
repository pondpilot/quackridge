# Install and configure QuackRidge

QuackRidge is experimental. Install only an archive linked from the signed
release manifest. PondPilot downloads an archive but cannot launch it, move it,
or grant it operating-system permissions.

## Verify and install an archive

Each release contains three native archives, `checksums.txt`, SPDX SBOMs, and
Sigstore bundles. Verify an archive before extracting it:

```sh
cosign verify-blob \
  --bundle quackridge_0.1.0_linux_amd64.tar.gz.sigstore.json \
  --certificate-identity-regexp '^https://github.com/pondpilot/quackridge/.github/workflows/release.yml@refs/tags/v.+' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  quackridge_0.1.0_linux_amd64.tar.gz
sha256sum -c quackridge_0.1.0_linux_amd64.tar.gz.sha256
```

Extract the archive and place its complete directory somewhere accessible only
to your user. Do not separate the executable from its `extensions` directory.
On macOS, the release also includes a Homebrew cask definition using the same
release-manifest URLs and hashes. Its publication to a third-party package
repository can lag the GitHub release. QuackRidge v0.1 does not publish a
Windows archive because DuckDB's Go/MinGW build cannot load the available MSVC
extension bundle.

## Create a read-only PostgreSQL role

Run the following as a PostgreSQL administrator, replacing the database, role,
schema, and generated password:

```sql
CREATE ROLE quackridge_reader LOGIN PASSWORD 'generated-password';
ALTER ROLE quackridge_reader SET default_transaction_read_only = on;
GRANT CONNECT ON DATABASE analytics TO quackridge_reader;
GRANT USAGE ON SCHEMA public TO quackridge_reader;
GRANT SELECT ON ALL TABLES IN SCHEMA public TO quackridge_reader;
ALTER DEFAULT PRIVILEGES IN SCHEMA public
  GRANT SELECT ON TABLES TO quackridge_reader;
```

Do not grant superuser, role creation, database creation, replication,
`BYPASSRLS`, or non-`SELECT` table privileges. QuackRidge's DuckDB attachment is
also read-only, but the PostgreSQL role remains the final authorization layer.

## Add and test a source

Passwords are accepted only through a masked terminal prompt or standard input;
there is intentionally no password flag.

```sh
quackridge source test postgres \
  --id warehouse --name Warehouse --alias warehouse \
  --host db.internal --port 5432 --database analytics \
  --user quackridge_reader --ssl-mode verify-full \
  --root-certificate-ref company-postgres-ca

quackridge source add postgres \
  --id warehouse --name Warehouse --alias warehouse \
  --host db.internal --port 5432 --database analytics \
  --user quackridge_reader --ssl-mode verify-full \
  --root-certificate-ref company-postgres-ca
```

For non-interactive CI only, use `--password-stdin`. Production credentials go
to macOS Keychain, Windows Credential Manager, or Linux Secret Service. They are
never stored in the JSON configuration. SSL modes are `disable`, `allow`,
`prefer`, `require`, `verify-ca`, and `verify-full`; production sources should
use `verify-full` whenever possible.

MySQL uses the same host, port, database, user, password-input, and SSL flags:

```sh
quackridge source add mysql --id commerce --name Commerce --alias commerce \
  --host db.internal --port 3306 --database commerce --user quackridge_reader \
  --ssl-mode required --password-stdin
```

SQLite and DuckDB files require an absolute path and no credential:

```sh
quackridge source add sqlite --id support --name Support --alias support \
  --path /absolute/path/support.sqlite
quackridge source add duckdb --id archive --name Archive --alias archive \
  --path /absolute/path/archive.duckdb
```

ODBC requires either a configured DSN or driver, a semantic database type, and
repeatable non-secret connection properties. The driver manager and driver must
already work for the QuackRidge process; on Linux and macOS this means unixODBC
must be installed. Credentials are accepted separately and never persisted in
the connection properties.

```sh
quackridge source add odbc --id support --name Support --alias support \
  --dsn support --database-type sqlserver --user quackridge_reader --password-stdin
```

## Run, diagnose, and pair

```sh
quackridge serve --extensions /path/to/quackridge/extensions
quackridge status
quackridge doctor --extensions /path/to/quackridge/extensions
quackridge pair
```

Keep `serve` in the foreground. Copy the temporary pairing URL and one-time code
into PondPilot. Pairing is loopback-only, origin-bound, expires after two
minutes by default, and stops accepting requests after its first success. Use
`quackridge pair --manual` only for explicit local development or recovery.

The data-plane token rotates when the daemon restarts. It can also be rotated
through the local control API; existing PondPilot attachments will disconnect
and must pair again. Raw tokens must not be placed in URLs, shell history, bug
reports, or screenshots.

## Locations and uninstall

The default configuration and control endpoint live under the operating
system's per-user application configuration directory in `QuackRidge/`:

- macOS: `~/Library/Application Support/QuackRidge/`
- Linux: `$XDG_CONFIG_HOME/QuackRidge/` or `~/.config/QuackRidge/`
- Windows: `%AppData%\QuackRidge\`

To uninstall, stop the foreground process, run `source remove <id>` for each
source so credentials are deleted from the platform store, remove the extracted
release directory, and remove the `QuackRidge` configuration directory. Also
remove the QuackRidge connection from PondPilot so its encrypted token record is
deleted. QuackRidge v0.1 installs no service, daemon registration, kernel
component, browser extension, or automatic updater.
