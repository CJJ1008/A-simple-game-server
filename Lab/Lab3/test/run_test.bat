@echo off
setlocal
cd /d "%~dp0"
go run .\runner\main.go %*
echo 清理端口 9310 9311 9312 9313...
for %%p in (9310 9311 9312 9313) do (
    for /f "tokens=5" %%i in ('netstat -aon ^| findstr ":%%p " 2^>nul') do (
        taskkill /F /PID %%i >nul 2>&1 && echo   已清理端口 %%p (pid=%%i)
    )
)
exit /b %errorlevel%