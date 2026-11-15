@echo off
rem Double-click to install puck-tak on the puck, or run from a Command Prompt:
rem   deploy.cmd             install / update
rem   deploy.cmd -Uninstall  remove (add -Purge to also delete packages)
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0deploy.ps1" %*
echo.
pause
