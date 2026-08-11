# Security Policy

QuackRidge is pre-release experimental software and currently has no supported
release. Please report suspected vulnerabilities privately through GitHub's
security-advisory form for `pondpilot/quackridge`; do not open a public issue.

QuackRidge binds its data plane to loopback, requires a rotating token, attaches
PostgreSQL sources read-only, and applies a parser-backed SQL policy. These are
defense-in-depth controls, not a substitute for a dedicated PostgreSQL role with
read-only grants. See `docs/security.md` for the trust boundary and denied SQL
surface.
