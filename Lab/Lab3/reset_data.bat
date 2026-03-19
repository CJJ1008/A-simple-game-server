@echo off
setlocal
cd /d "%~dp0"
go run .\reset_data.go %*
exit /b %errorlevel%
