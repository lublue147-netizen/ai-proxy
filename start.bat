@echo off
cd /d "%~dp0"
title AI Proxy Service

if not exist "config.yaml" goto NO_CONFIG
if not exist "ai-proxy.exe" goto NO_EXE

echo [AI Proxy] Starting local proxy service...
echo [AI Proxy] Press Ctrl+C to stop.
echo.
ai-proxy.exe
if errorlevel 1 goto ERROR
goto END

:NO_CONFIG
if exist "config.example.yaml" copy "config.example.yaml" "config.yaml" >nul
echo [AI Proxy] Created config.yaml. Please edit config.yaml and restart.
pause
exit /b 1

:NO_EXE
echo [AI Proxy] Error: ai-proxy.exe not found!
pause
exit /b 1

:ERROR
echo.
echo [AI Proxy] Proxy process exited with error.
pause
exit /b 1

:END
pause
