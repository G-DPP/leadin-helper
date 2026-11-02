The LeadIn helper starts Claude Code on your computer for the LeadIn website.

## Which file

| Your computer | Take this file |
|---|---|
| Mac with Apple chip (M1 or later) | `leadin-helper_{version}_darwin_arm64.zip` |
| Mac with Intel chip | `leadin-helper_{version}_darwin_amd64.zip` |
| Linux | `leadin-helper_{version}_linux_amd64.zip` |
| Windows | `leadin-helper_{version}_windows_amd64.exe` |

The files without `.zip` are the same binaries, for those who install from a terminal.

These binaries are not code-signed: macOS and Windows will warn you. LeadIn's setup page explains what to do.

## Verify

```bash
sha256sum -c --ignore-missing SHA256SUMS
gh attestation verify leadin-helper_{version}_linux_amd64 --repo G-DPP/leadin-helper
scripts/verify-release.sh {version} https://github.com/G-DPP/leadin-helper/releases/download/v{version}/SHA256SUMS
```

See [Verify a release](https://github.com/G-DPP/leadin-helper#verify-a-release) in the README.
