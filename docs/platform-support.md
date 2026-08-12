# Platform support

QuackRidge v0.1 targets:

| Operating system | Architecture | Status |
| --- | --- | --- |
| macOS | AMD64 | release target, native build smoke passed |
| macOS | ARM64 | release target, native build smoke passed |
| Linux | AMD64 | release target, native build and PostgreSQL smoke passed |
| Windows | AMD64 | unsupported follow-up: Go uses MinGW but the pinned extensions are MSVC-only |
| Linux | ARM64 | unsupported follow-up target |
| Windows | ARM64 | unsupported follow-up target |

An archive is unsupported until the exact archive passes a native identity,
source-attach, query, and clean-shutdown smoke test. CI must block publication of
an asset whose native smoke job did not run and pass.
