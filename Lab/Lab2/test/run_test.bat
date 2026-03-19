@echo off
setlocal
cd /d "%~dp0"
go run .\runner\main.go %*
exit /b %errorlevel%
