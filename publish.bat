@echo off
REM Veroeffentlicht diesen Ordner als PRIVATES GitHub-Repo mit dem lokalen gh.
cd /d "%~dp0"
where gh >nul 2>nul || (echo gh wurde nicht gefunden. & pause & exit /b 1)
gh auth status >nul 2>nul || gh auth login
for /f %%i in ('gh api user --jq .login') do set GHLOGIN=%%i
for /f %%i in ('gh api user --jq .id') do set GHID=%%i
git config user.name "%GHLOGIN%"
git config user.email "%GHID%+%GHLOGIN%@users.noreply.github.com"
git commit --amend --reset-author --no-edit
gh repo create deadlock-counter-build --private --source . --remote origin --push
echo.
echo Fertig. Das Repo ist privat.
pause
