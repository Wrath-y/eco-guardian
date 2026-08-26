[CmdletBinding()]
param(
    [ValidateRange(1, 100)] [int] $RepeatCount = 20
)

$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'

function Assert-Condition([bool] $Condition, [string] $Message) {
    if (-not $Condition) { throw $Message }
}

function Invoke-Checked([string] $FilePath, [string[]] $CommandArguments) {
    Write-Host "> $FilePath $($CommandArguments -join ' ')"
    & $FilePath @CommandArguments
    if ($LASTEXITCODE -ne 0) {
        throw "command failed with exit code $LASTEXITCODE`: $FilePath $($CommandArguments -join ' ')"
    }
}

Assert-Condition ($env:OS -eq 'Windows_NT') 'the Windows runtime suite must run on Windows'
Assert-Condition ([System.Environment]::Is64BitOperatingSystem) 'a 64-bit Windows operating system is required'
Assert-Condition ($env:PROCESSOR_ARCHITECTURE -eq 'AMD64') 'a native x64 Windows process is required; ARM x64 emulation is supplemental only'

$go = Get-Command 'go.exe' -ErrorAction Stop
$gcc = Get-Command 'gcc.exe' -ErrorAction SilentlyContinue
$gccPath = $(if ($gcc) { $gcc.Source } else { $null })
if (-not $gccPath) {
    foreach ($candidate in @('C:\mingw64\bin\gcc.exe', 'C:\msys64\mingw64\bin\gcc.exe')) {
        if (Test-Path -LiteralPath $candidate -PathType Leaf) {
            $gccPath = $candidate
            break
        }
    }
}
Assert-Condition (-not [string]::IsNullOrWhiteSpace($gccPath)) 'MinGW-w64 gcc is required for the Windows Go race detector'

$env:CC = $gccPath
$env:CGO_ENABLED = '1'
Write-Host "Go: $(& $go.Source version)"
Write-Host "C compiler: $env:CC"

# Full native Windows coverage first, including build-tagged process and Job Object tests.
Invoke-Checked $go.Source @('test', './...', '-timeout=45m')

# The race gate concentrates on process ownership, supervision, lifecycle composition,
# action idempotency, and status publication where Windows callbacks overlap.
Invoke-Checked $go.Source @(
    'test', '-race',
    './internal/platform/process',
    './internal/app/runtime/graphprocess',
    './internal/bootstrap',
    './internal/httpapi',
    '-timeout=45m'
)

# Repeat native handle/Job Object and supervisor crash/shutdown cases to catch leaks
# and generation races that a single run can miss.
Invoke-Checked $go.Source @(
    'test',
    './internal/platform/process',
    './internal/app/runtime/graphprocess',
    '-run', 'TestWindows',
    "-count=$RepeatCount",
    '-timeout=45m'
)

# Repeat listener, worker, log, SSE, and SQLite close/reopen boundaries. The regular
# expression is intentionally shared across packages; packages with no match pass.
$resourcePattern = 'Test(ShutdownRunsSafePointsWorkersDependenciesAndStoresInOrder|ShutdownIsBoundedAndRepeatedCloseReturnsSameOutcome|GraphRuntimeDependencyCloseCancelsAndJoinsRefreshWorker|RuntimeStopIsConcurrentAndIdempotent|RuntimeActionsEnforcePreconditionsAndIdempotency|LoggerRotatesReopensFlushesAndCorrelatesHTTP|VersionHandlerResumesPersistedJobEventsAndCompletesTerminalStream|ConcurrentRiskReportReadsAndProjectReopen|UnresolvedHistoricalRevisionAndCloseReopenEquivalence)'
Invoke-Checked $go.Source @(
    'test',
    './internal/bootstrap',
    './internal/app/runtime/diagnostics',
    './internal/httpapi',
    './internal/storage/sqlite',
    '-run', $resourcePattern,
    "-count=$RepeatCount",
    '-timeout=45m'
)

Write-Host "Windows runtime gate passed (repeat count: $RepeatCount)."
