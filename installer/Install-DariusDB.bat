@echo off
title Darius DB Installer
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0install.ps1" %*
if errorlevel 1 (
  echo.
  echo Installer exited with an error.
  pause
)
