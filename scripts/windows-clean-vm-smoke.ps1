[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)] [string] $CompleteArtifact,
    [Parameter(Mandatory = $true)] [string] $LightweightArtifact,
    [Parameter(Mandatory = $true)] [string] $FixtureExecutable,
    [Parameter(Mandatory = $true)] [string] $ReportPath,
    [ValidateRange(10, 300)] [int] $StartupTimeoutSeconds = 90,
    [switch] $AllowElevated,
    [switch] $AllowDevelopmentTools
)

$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'

function Assert-Condition([bool] $Condition, [string] $Message) {
    if (-not $Condition) { throw $Message }
}

function Write-Utf8NoBom([string] $Path, [string] $Value) {
    $encoding = New-Object System.Text.UTF8Encoding($false)
    [System.IO.File]::WriteAllText($Path, $Value, $encoding)
}

function Resolve-PackageRoot([string] $Artifact, [string] $Destination) {
    $resolved = (Resolve-Path -LiteralPath $Artifact).Path
    if ((Get-Item -LiteralPath $resolved).PSIsContainer) {
        return $resolved
    }
    Assert-Condition ($resolved.EndsWith('.zip', [System.StringComparison]::OrdinalIgnoreCase)) "artifact must be a directory or zip: $resolved"
    Expand-Archive -LiteralPath $resolved -DestinationPath $Destination
    $manifests = @(Get-ChildItem -LiteralPath $Destination -Filter 'package-manifest.json' -File -Recurse)
    Assert-Condition ($manifests.Count -eq 1) "archive must contain exactly one package-manifest.json: $resolved"
    return $manifests[0].Directory.FullName
}

function Test-PackageManifest([string] $Root, [string] $ExpectedMode) {
    $manifestPath = Join-Path $Root 'package-manifest.json'
    Assert-Condition (Test-Path -LiteralPath $manifestPath -PathType Leaf) "package manifest is missing"
    $manifest = Get-Content -LiteralPath $manifestPath -Raw | ConvertFrom-Json
    Assert-Condition ($manifest.schema_version -eq 1) "unexpected package manifest schema"
    Assert-Condition ($manifest.package_mode -eq $ExpectedMode) "unexpected package mode"
    Assert-Condition ($manifest.supported_platform.os -eq 'windows' -and $manifest.supported_platform.architecture -eq 'amd64') "unexpected package platform"

    $eco = Join-Path $Root ([string]$manifest.eco_guardian.executable).Replace('/', '\')
    Assert-Condition (Test-Path -LiteralPath $eco -PathType Leaf) "eco-guardian executable is missing"
    $ecoFile = Get-Item -LiteralPath $eco
    $ecoHash = (Get-FileHash -LiteralPath $eco -Algorithm SHA256).Hash.ToLowerInvariant()
    Assert-Condition ($ecoFile.Length -eq [int64]$manifest.eco_guardian.size_bytes -and $ecoHash -eq $manifest.eco_guardian.sha256) "eco-guardian executable digest mismatch"

    foreach ($component in @($manifest.components)) {
        foreach ($file in @($component.files)) {
            $componentPath = Join-Path $Root ([string]$file.path).Replace('/', '\')
            Assert-Condition (Test-Path -LiteralPath $componentPath -PathType Leaf) "manifest component file is missing: $($file.path)"
            $componentFile = Get-Item -LiteralPath $componentPath
            $componentHash = (Get-FileHash -LiteralPath $componentPath -Algorithm SHA256).Hash.ToLowerInvariant()
            Assert-Condition ($componentFile.Length -eq [int64]$file.size_bytes -and $componentHash -eq $file.sha256) "manifest component digest mismatch: $($file.path)"
        }
    }

    $embeddedNames = @($manifest.embedded_asset_digests.PSObject.Properties.Name)
    Assert-Condition ($embeddedNames -contains 'web/dist/index.html') "embedded UI identity is missing"
    Assert-Condition (@($embeddedNames | Where-Object { $_ -like 'migrations/*' }).Count -gt 0) "embedded migrations identity is missing"
    Assert-Condition (@($embeddedNames | Where-Object { $_ -like 'compiled/templates/*' }).Count -gt 0) "embedded deterministic templates identity is missing"
    if ($ExpectedMode -eq 'complete') {
        $kinds = @($manifest.components | ForEach-Object { $_.kind })
        foreach ($kind in @('local-rag', 'python-runtime', 'embedding-model', 'rerank-model')) {
            Assert-Condition ($kinds -contains $kind) "complete package component is missing: $kind"
        }
    } else {
        Assert-Condition (@($manifest.components).Count -eq 0) "lightweight package contains bundled components"
        foreach ($forbidden in @('local-rag', 'python', 'models')) {
            Assert-Condition (-not (Test-Path -LiteralPath (Join-Path $Root $forbidden))) "lightweight package contains $forbidden"
        }
    }
    return @{ Manifest = $manifest; Executable = $eco }
}

function Wait-ListenerURL([System.Diagnostics.Process] $Process, [string] $StdoutPath, [int] $TimeoutSeconds) {
    $deadline = [DateTime]::UtcNow.AddSeconds($TimeoutSeconds)
    while ([DateTime]::UtcNow -lt $deadline) {
        if ($Process.HasExited) { throw "eco-guardian exited before publishing its listener URL (exit $($Process.ExitCode))" }
        if (Test-Path -LiteralPath $StdoutPath) {
            $content = Get-Content -LiteralPath $StdoutPath -Raw
            $match = [regex]::Match($content, 'http://127\.0\.0\.1:[0-9]{1,5}')
            if ($match.Success) { return $match.Value }
        }
        Start-Sleep -Milliseconds 200
    }
    throw 'timed out waiting for the Eco Guardian listener URL'
}

function Get-DescendantProcessIds([int] $RootPID) {
    $found = New-Object 'System.Collections.Generic.HashSet[int]'
    $frontier = @($RootPID)
    while ($frontier.Count -gt 0) {
        $next = @()
        foreach ($parent in $frontier) {
            foreach ($child in @(Get-CimInstance Win32_Process -Filter "ParentProcessId=$parent")) {
                $pidValue = [int]$child.ProcessId
                if ($found.Add($pidValue)) { $next += $pidValue }
            }
        }
        $frontier = $next
    }
    return @($found)
}

function Wait-NoProcesses([int[]] $ProcessIds, [int] $TimeoutSeconds) {
    $deadline = [DateTime]::UtcNow.AddSeconds($TimeoutSeconds)
    do {
        $alive = @($ProcessIds | Where-Object { Get-Process -Id $_ -ErrorAction SilentlyContinue })
        if ($alive.Count -eq 0) { return }
        Start-Sleep -Milliseconds 250
    } while ([DateTime]::UtcNow -lt $deadline)
    throw "owned descendant processes remain after Eco Guardian exit: $($alive -join ',')"
}

function Invoke-PackageSmoke([string] $Mode, [string] $PackageRoot, [string] $RunRoot) {
    $verified = Test-PackageManifest $PackageRoot $Mode
    $localData = Join-Path $RunRoot 'local-app-data'
    $projectPath = Join-Path $RunRoot 'project'
    $ecoData = Join-Path $localData 'EcoGuardian'
    New-Item -ItemType Directory -Path $RunRoot -Force | Out-Null
    New-Item -ItemType Directory -Path $localData -Force | Out-Null
    New-Item -ItemType Directory -Path $ecoData -Force | Out-Null

    $oldLocalAppData = $env:LOCALAPPDATA
    $oldCredential = $env:ECO_GUARDIAN_OPENAI_API_KEY
    $credentialCanary = 'clean-vm-secret-canary'
    $process = $null
    $descendants = @()
    try {
        $env:LOCALAPPDATA = $localData
        $env:ECO_GUARDIAN_OPENAI_API_KEY = $credentialCanary
        $fixtureOutput = & $FixtureExecutable --local-app-data $localData --project $projectPath
        Assert-Condition ($LASTEXITCODE -eq 0) "project fixture preparation failed"
        $fixture = $fixtureOutput | ConvertFrom-Json
        Assert-Condition ($fixture.database_size -gt 0) "project fixture database is empty"

        $settingsPath = Join-Path $ecoData 'settings.json'
        $templatePath = Join-Path $PackageRoot 'defaults\settings.template.json'
        $settings = Get-Content -LiteralPath $templatePath -Raw | ConvertFrom-Json
        $settings.browser.auto_open = $false
        $settings.graph.mode = $(if ($Mode -eq 'complete') { 'bundled' } else { 'disabled' })
        $settings.graph.endpoint = ''
        Write-Utf8NoBom $settingsPath ($settings | ConvertTo-Json -Depth 16 -Compress)

        $stdoutPath = Join-Path $RunRoot 'eco.stdout.log'
        $stderrPath = Join-Path $RunRoot 'eco.stderr.log'
        $process = Start-Process -FilePath $verified.Executable -WorkingDirectory $PackageRoot -ArgumentList @('--browser-auto-open', 'false') -PassThru -RedirectStandardOutput $stdoutPath -RedirectStandardError $stderrPath
        $listenerURL = Wait-ListenerURL $process $stdoutPath $StartupTimeoutSeconds
        Assert-Condition ($listenerURL -match '^http://127\.0\.0\.1:[0-9]{1,5}$') "listener URL is not loopback-only"

        $healthResponse = Invoke-WebRequest -UseBasicParsing -Uri ($listenerURL + '/health') -TimeoutSec 10
        Assert-Condition ($healthResponse.StatusCode -eq 200) "Eco health endpoint is unavailable"
        $uiResponse = Invoke-WebRequest -UseBasicParsing -Uri ($listenerURL + '/') -TimeoutSec 10
        Assert-Condition ($uiResponse.StatusCode -eq 200 -and $uiResponse.Content -match '<div id="app"') "embedded UI is unavailable"

        $statusDeadline = [DateTime]::UtcNow.AddSeconds(15)
        do {
            $status = Invoke-RestMethod -Method Get -Uri ($listenerURL + '/api/v1/runtime/status') -TimeoutSec 10
            if ($status.project.state -eq 'active') { break }
            Start-Sleep -Milliseconds 200
        } while ([DateTime]::UtcNow -lt $statusDeadline)
        Assert-Condition ($status.schema_version -eq 1 -and $status.listener.url -eq $listenerURL) "runtime status identity mismatch"
        Assert-Condition ($status.build.package_mode -eq $Mode) "runtime package mode mismatch"
        Assert-Condition ($status.project.state -eq 'active' -and $status.project.project_id -eq $fixture.project_id) "recent project was not reopened"
        Assert-Condition ($status.log_location -eq '<project-directory>/logs') "runtime exposed an unsafe log path"
        if ($status.process.endpoint) {
            Assert-Condition ($status.process.endpoint -match '^http://(127\.0\.0\.1|localhost):[0-9]{1,5}$') "Graph endpoint is not loopback-only"
        }
        if ($Mode -eq 'complete') {
            Assert-Condition ($status.process.ownership -eq 'bundled' -and $status.process.state -eq 'ready') "complete package did not supervise a ready bundled Graph process"
            $graphObservation = @($status.dependencies | Where-Object { $_.id -eq 'graph.sync' })
            Assert-Condition ($graphObservation.Count -eq 1 -and $graphObservation[0].state -eq 'healthy') "complete package Graph observation is not healthy"
        } else {
            Assert-Condition ($status.process.ownership -eq 'not_selected') "lightweight package unexpectedly selected an owned Graph process"
        }
        $aiCapability = @($status.capabilities | Where-Object { $_.id -eq 'ai.design' })
        Assert-Condition ($aiCapability.Count -eq 1 -and $aiCapability[0].state -eq 'unavailable') "AI capability should remain unavailable without a configured model"

        $listeners = @(Get-NetTCPConnection -State Listen -OwningProcess $process.Id -ErrorAction Stop)
        Assert-Condition ($listeners.Count -gt 0) "Eco Guardian has no listening socket"
        foreach ($listener in $listeners) {
            Assert-Condition ($listener.LocalAddress -eq '127.0.0.1') "Eco Guardian has a non-loopback listener: $($listener.LocalAddress)"
        }
        $logPath = Join-Path $PackageRoot 'logs\eco-guardian.log'
        $logDeadline = [DateTime]::UtcNow.AddSeconds(10)
        while (-not (Test-Path -LiteralPath $logPath -PathType Leaf) -and [DateTime]::UtcNow -lt $logDeadline) { Start-Sleep -Milliseconds 200 }
        Assert-Condition (Test-Path -LiteralPath $logPath -PathType Leaf) "runtime log file was not created"
        $logBody = Get-Content -LiteralPath $logPath -Raw
        Assert-Condition (-not $logBody.Contains($credentialCanary) -and -not $logBody.Contains($projectPath) -and -not $logBody.Contains($localData)) "runtime log contains a credential or raw user path"

        $descendants = Get-DescendantProcessIds $process.Id
        if ($Mode -eq 'complete') { Assert-Condition ($descendants.Count -gt 0) "complete package has no owned child process" }
        if ($Mode -eq 'lightweight') { Assert-Condition ($descendants.Count -eq 0) "lightweight package started a child process" }

        Stop-Process -Id $process.Id -Force
        $process.WaitForExit(10000) | Out-Null
        Wait-NoProcesses $descendants 15
        $process = $null

        $reopenOutput = & $FixtureExecutable --local-app-data $localData --project $projectPath --reopen-only
        Assert-Condition ($LASTEXITCODE -eq 0) "project could not be reopened after Eco Guardian exit"
        $reopened = $reopenOutput | ConvertFrom-Json
        Assert-Condition ($reopened.reopened -eq $true -and $reopened.project_id -eq $fixture.project_id -and $reopened.database_size -gt 0) "project identity or persistence changed"

        return [ordered]@{
            mode = $Mode
            package_root = '<smoke-workspace>/' + $Mode
            listener = $listenerURL
            phase = $status.phase
            process_ownership = $status.process.ownership
            process_state = $status.process.state
            project_id = $fixture.project_id
            project_database_size = [int64]$fixture.database_size
            owned_descendant_count = $descendants.Count
            exit_mode = 'forced_parent_termination'
            checks = @('manifest_hashes', 'embedded_ui', 'embedded_migrations', 'embedded_templates', 'loopback_listener', 'runtime_status', 'project_reopen', 'logs', 'exit_cleanup')
        }
    } finally {
        if ($process -and -not $process.HasExited) {
            Stop-Process -Id $process.Id -Force -ErrorAction SilentlyContinue
            $process.WaitForExit(5000) | Out-Null
        }
        $env:LOCALAPPDATA = $oldLocalAppData
        $env:ECO_GUARDIAN_OPENAI_API_KEY = $oldCredential
    }
}

$report = [ordered]@{
    schema_version = 1
    started_at = [DateTime]::UtcNow.ToString('o')
    machine = [ordered]@{}
    packages = @()
    success = $false
    error = $null
}
$workRoot = Join-Path ([System.IO.Path]::GetTempPath()) ('eco-guardian-clean-vm-' + [Guid]::NewGuid().ToString('N'))
try {
    $os = Get-CimInstance Win32_OperatingSystem
    $version = [System.Environment]::OSVersion.Version
    Assert-Condition ([System.Environment]::Is64BitOperatingSystem) 'Windows x64 is required'
    Assert-Condition ($env:PROCESSOR_ARCHITECTURE -eq 'AMD64') 'native Windows x64 is required; ARM x64 emulation is supplemental only'
    Assert-Condition ($version.Major -eq 10 -and $version.Build -ge 22000) 'Windows 11 is required'
    $isElevated = ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
    if (-not $AllowElevated) { Assert-Condition (-not $isElevated) 'smoke suite must run without elevation; pass -AllowElevated only for diagnostic runs' }
    $developmentTools = @()
    foreach ($toolName in @('go.exe', 'node.exe', 'gcc.exe')) {
        if (Get-Command $toolName -ErrorAction SilentlyContinue) { $developmentTools += $toolName }
    }
    if (-not $AllowDevelopmentTools) { Assert-Condition ($developmentTools.Count -eq 0) "development tools found on PATH: $($developmentTools -join ',')" }
    Assert-Condition (Test-Path -LiteralPath $FixtureExecutable -PathType Leaf) 'Windows smoke fixture executable is missing'

    New-Item -ItemType Directory -Path $workRoot | Out-Null
    $completeRoot = Resolve-PackageRoot $CompleteArtifact (Join-Path $workRoot 'complete-package')
    $lightweightRoot = Resolve-PackageRoot $LightweightArtifact (Join-Path $workRoot 'lightweight-package')
    $report.machine = [ordered]@{
        caption = $os.Caption
        version = $version.ToString()
        windows_release = 'windows-11'
        architecture = 'amd64'
        elevated = $isElevated
        development_tools_on_path = @($developmentTools)
    }
    $report.packages = @(
        Invoke-PackageSmoke 'complete' $completeRoot (Join-Path $workRoot 'complete-run')
        Invoke-PackageSmoke 'lightweight' $lightweightRoot (Join-Path $workRoot 'lightweight-run')
    )
    $report.success = $true
} catch {
    $report.error = $_.Exception.Message
} finally {
    $report.completed_at = [DateTime]::UtcNow.ToString('o')
    $reportDirectory = Split-Path -Parent $ReportPath
    if ($reportDirectory) { New-Item -ItemType Directory -Path $reportDirectory -Force | Out-Null }
    Write-Utf8NoBom $ReportPath ($report | ConvertTo-Json -Depth 16)
    if (Test-Path -LiteralPath $workRoot) { Remove-Item -LiteralPath $workRoot -Recurse -Force }
}

if (-not $report.success) {
    Write-Error "clean-VM smoke failed: $($report.error); report: $ReportPath"
    exit 1
}
Write-Host "clean-VM smoke passed; report: $ReportPath"
