# Windows clean-VM acceptance

Run the packaged-artifact smoke suite on a Windows 11 x64 client VM. The VM must have no Go, Node.js, GCC, repository checkout, or administrator elevation. Windows PowerShell 5.1 and the built-in networking/CIM cmdlets are sufficient. A Windows Server CI run and Windows 11 ARM x64 emulation are useful supplemental checks, but neither satisfies this client-VM release gate.

Prepare these files on the build machine, then copy only them to the VM:

- the complete-package zip;
- the lightweight-package zip;
- `windows-smoke-fixture.exe`, cross-compiled with:

```sh
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o windows-smoke-fixture.exe ./internal/packageinfo/cmd/windows-smoke-fixture
```

Copy `scripts/windows-clean-vm-smoke.ps1` alongside those files and run it from a non-elevated PowerShell prompt:

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\windows-clean-vm-smoke.ps1 `
  -CompleteArtifact .\eco-guardian-complete.zip `
  -LightweightArtifact .\eco-guardian-lightweight.zip `
  -FixtureExecutable .\windows-smoke-fixture.exe `
  -ReportPath .\reports\windows-11-x64.json
```

A passing report verifies the native x64 environment, package and executable hashes, embedded UI/migration/template identities, loopback-only serving, complete-package bundled Graph readiness, lightweight no-child behavior, AI-unavailable projection, recent-project reopen/persistence, bounded safe logs, and owned-descendant cleanup after forced application exit.

`-AllowElevated` and `-AllowDevelopmentTools` exist only for diagnostic runs. Reports produced with either exception do not satisfy task 12.6.
