# Platform support

QuackRidge v1 targets:

| Operating system | Architecture | Status |
| --- | --- | --- |
| macOS | AMD64 | release target, pending native smoke test |
| macOS | ARM64 | release target, pending native smoke test |
| Linux | AMD64 | release target, pending native smoke test |
| Windows | AMD64 | release target, pending native smoke test |
| Linux | ARM64 | unsupported follow-up target |
| Windows | ARM64 | unsupported follow-up target |

An archive is unsupported until the exact archive passes a native identity,
source-attach, query, and clean-shutdown smoke test. CI must block publication of
an asset whose native smoke job did not run and pass.
