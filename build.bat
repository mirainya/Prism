@echo off
setlocal

echo ========================================
echo   Prism Build Script - Linux AMD64
echo ========================================

rem Build settings
set "APP_NAME=prism"
set "OUTPUT_DIR=dist"
set "VERSION=2.0.0"

rem Use an RFC3339 UTC timestamp without spaces so linker flags remain stable.
for /f %%I in ('powershell.exe -NoProfile -Command "(Get-Date).ToUniversalTime().ToString('yyyy-MM-ddTHH:mm:ssZ')"') do set "BUILD_TIME=%%I"
if not defined BUILD_TIME (
    echo [ERROR] Unable to determine build time.
    exit /b 1
)
for /f %%I in ('git rev-parse HEAD 2^>nul') do set "SOURCE_REVISION=%%I"
if not defined SOURCE_REVISION set "SOURCE_REVISION=working-tree"

echo.
echo [1/4] Cleaning output directory...
if exist "%OUTPUT_DIR%" rd /s /q "%OUTPUT_DIR%"
mkdir "%OUTPUT_DIR%"
if not "%ERRORLEVEL%"=="0" exit /b 1

echo.
echo [2/4] Installing frontend dependencies...
pushd console
if not "%ERRORLEVEL%"=="0" exit /b 1
call npm ci
if not "%ERRORLEVEL%"=="0" (
    echo.
    echo [ERROR] npm ci failed!
    popd
    exit /b 1
)

echo.
echo [3/4] Building frontend (will be embedded in binary)...
call npm run build
if not "%ERRORLEVEL%"=="0" (
    echo.
    echo [ERROR] Frontend build failed!
    popd
    exit /b 1
)
popd

echo.
echo [4/4] Building backend for Linux AMD64 (with embedded console)...
set "GOOS=linux"
set "GOARCH=amd64"
set "CGO_ENABLED=0"

go build -trimpath -ldflags="-s -w -X main.Version=%VERSION% -X main.BuildTime=%BUILD_TIME% -X github.com/mirainya/Prism/internal/gateway/adapter.BuildRevision=%SOURCE_REVISION%" -o "%OUTPUT_DIR%\%APP_NAME%" ./cmd/server

if not "%ERRORLEVEL%"=="0" (
    echo.
    echo [ERROR] Backend build failed!
    exit /b 1
)

rem Include a configuration template in the package.
mkdir "%OUTPUT_DIR%\configs"
if not "%ERRORLEVEL%"=="0" exit /b 1
if exist "configs\config.example.yaml" copy /y "configs\config.example.yaml" "%OUTPUT_DIR%\configs\config.example.yaml" >nul
if not "%ERRORLEVEL%"=="0" exit /b 1

echo.
echo ========================================
echo   Build completed successfully!
echo ========================================
echo   Output:  %OUTPUT_DIR%/%APP_NAME%
echo   Config:  %OUTPUT_DIR%/configs/config.example.yaml
echo   OS:      linux
echo   Arch:    amd64
echo   Version: %VERSION%
echo   Time:    %BUILD_TIME%
echo ========================================
echo.
echo   Deploy:
echo   1. Upload prism and configs/ to server
echo   2. Rename config.example.yaml to config.yaml
echo   3. Edit config.yaml with your settings
echo   4. Run: ./prism
echo ========================================

endlocal
