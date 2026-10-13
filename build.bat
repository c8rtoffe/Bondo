@echo off
powershell.exe -NoProfile -ExecutionPolicy Bypass -File "%~dp0build.ps1"
set "build_result=%ERRORLEVEL%"
if /I not "%~1"=="--no-pause" pause
exit /b %build_result%
