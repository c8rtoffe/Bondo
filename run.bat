@echo off
cd /d "%~dp0"
if exist "build\bin\bondo.exe" (
    start "" "build\bin\bondo.exe"
) else (
    echo Executable not found. Please run build.bat first.
    pause
)
