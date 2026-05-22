@echo off
echo Building Sypora...
echo.
echo [1/2] Generating Windows resource (icon)...
rsrc -ico assets/icon.ico -o rsrc.syso
if %ERRORLEVEL% NEQ 0 (
    echo Warning: rsrc failed, continuing without embedded icon.
)
echo.
echo [2/2] Building executable...
go build -ldflags "-H windowsgui -s -w" -o sypora.exe .
if %ERRORLEVEL% EQU 0 (
    echo.
    echo Build successful: sypora.exe
) else (
    echo.
    echo Build failed.
)
pause
