@echo off
setlocal EnableExtensions EnableDelayedExpansion

set "SCRIPT_DIR=%~dp0"
if defined LOCAL_RAG_DIR (
    set "LOCAL_RAG_ROOT=%LOCAL_RAG_DIR%"
) else (
    set "LOCAL_RAG_ROOT=%SCRIPT_DIR%..\local-rag"
)
if defined ECO_GUARDIAN_GRAPH_ENDPOINT (
    set "GRAPH_ENDPOINT=%ECO_GUARDIAN_GRAPH_ENDPOINT%"
) else (
    set "GRAPH_ENDPOINT=http://127.0.0.1:8765"
)
if "!GRAPH_ENDPOINT:~-1!"=="/" set "GRAPH_ENDPOINT=!GRAPH_ENDPOINT:~0,-1!"
if defined ECO_GUARDIAN_RUNTIME_DIR (
    set "RUNTIME_DIR=%ECO_GUARDIAN_RUNTIME_DIR%"
) else (
    set "RUNTIME_DIR=%SCRIPT_DIR%.run"
)
set "ECO_GUARDIAN_BINARY=!RUNTIME_DIR!\eco-guardian.exe"
set "STARTED_LOCAL_RAG=0"

where go >nul 2>&1
if !errorlevel! neq 0 (
    echo Error: Go is required to start Eco Guardian. 1>&2
    exit /b 1
)
where npm.cmd >nul 2>&1
if !errorlevel! neq 0 (
    echo Error: npm is required to build the Eco Guardian web UI. 1>&2
    exit /b 1
)
where curl.exe >nul 2>&1
if !errorlevel! neq 0 (
    echo Error: curl is required to check local-rag health. 1>&2
    exit /b 1
)

if not exist "!RUNTIME_DIR!" mkdir "!RUNTIME_DIR!"
echo Building Eco Guardian web UI
call npm.cmd --prefix "%SCRIPT_DIR%web" run build
if !errorlevel! neq 0 exit /b !errorlevel!
echo Building Eco Guardian
go build -o "!ECO_GUARDIAN_BINARY!" .\cmd\eco-guardian
if !errorlevel! neq 0 exit /b !errorlevel!

curl.exe --fail --silent --show-error --max-time 3 "!GRAPH_ENDPOINT!/health" >nul 2>&1
if !errorlevel! equ 0 (
    echo local-rag is already healthy at !GRAPH_ENDPOINT!
) else (
    if not exist "!LOCAL_RAG_ROOT!\start.bat" (
        echo Error: local-rag launcher not found at !LOCAL_RAG_ROOT!\start.bat 1>&2
        echo Set LOCAL_RAG_DIR to the local-rag repository path. 1>&2
        exit /b 1
    )
    echo Starting local-rag from !LOCAL_RAG_ROOT!
    call "!LOCAL_RAG_ROOT!\start.bat"
    set "LOCAL_RAG_EXIT=!errorlevel!"
    cd /d "%SCRIPT_DIR%"
    if !LOCAL_RAG_EXIT! neq 0 exit /b !LOCAL_RAG_EXIT!
    set "STARTED_LOCAL_RAG=1"
)

curl.exe --fail --silent --show-error --max-time 3 "!GRAPH_ENDPOINT!/health" >nul 2>&1
if !errorlevel! neq 0 (
    echo Error: local-rag did not become healthy at !GRAPH_ENDPOINT! 1>&2
    if "!STARTED_LOCAL_RAG!"=="1" call "!LOCAL_RAG_ROOT!\stop.bat"
    exit /b 1
)

echo Starting Eco Guardian with Graph endpoint !GRAPH_ENDPOINT!
cd /d "%SCRIPT_DIR%"
if not defined GIN_MODE set "GIN_MODE=release"
"!ECO_GUARDIAN_BINARY!" --graph-endpoint "!GRAPH_ENDPOINT!" %*
set "ECO_GUARDIAN_EXIT=!errorlevel!"
if "!STARTED_LOCAL_RAG!"=="1" (
    call "!LOCAL_RAG_ROOT!\stop.bat"
    cd /d "%SCRIPT_DIR%"
)
endlocal & exit /b %ECO_GUARDIAN_EXIT%
