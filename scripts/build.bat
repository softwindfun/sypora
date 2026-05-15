@echo off
echo Building Sypora...
go build -ldflags "-H windowsgui -s -w" -o sypora.exe .
if %ERRORLEVEL% EQU 0 (
    echo Build successful: sypora.exe
) else (
    echo Build failed.
)
pause
