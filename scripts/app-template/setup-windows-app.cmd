@echo off
rem Turns a Windows 10 or 11 VM with the Quicksand agent into an app template
rem (e.g. for Edge): it signs a dedicated, non-administrator account in
rem automatically and turns off what would cover the app or interrupt it.
rem See "On Windows" in docs/app-templates.md.
rem
rem Usage, as an administrator (or SYSTEM, through the agent):
rem   setup-windows-app.cmd <password for the qsapp account, up to 14 characters>
setlocal
set PW=%~1
if "%PW%"=="" (
  echo usage: setup-windows-app.cmd ^<password^>
  exit /b 2
)

rem The account apps run as, signed in automatically.
net user qsapp >nul 2>&1 || net user qsapp %PW% /add /passwordchg:no /expires:never || exit /b 1
rem /expires:never is for the account; its password mustn't expire either,
rem or automatic sign-in stops working.
powershell -NoProfile -Command "Set-LocalUser -Name qsapp -PasswordNeverExpires $true" || exit /b 1
set W=HKLM\SOFTWARE\Microsoft\Windows NT\CurrentVersion\Winlogon
reg add "%W%" /v AutoAdminLogon /t REG_SZ /d 1 /f >nul
reg add "%W%" /v DefaultUserName /t REG_SZ /d qsapp /f >nul
reg add "%W%" /v DefaultDomainName /t REG_SZ /d . /f >nul
reg add "%W%" /v DefaultPassword /t REG_SZ /d %PW% /f >nul

rem No first sign-in animation, privacy questions or Edge welcome pages.
reg add HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\Policies\System /v EnableFirstLogonAnimation /t REG_DWORD /d 0 /f >nul
reg add HKLM\SOFTWARE\Policies\Microsoft\Windows\OOBE /v DisablePrivacyExperience /t REG_DWORD /d 1 /f >nul
reg add HKLM\SOFTWARE\Policies\Microsoft\Edge /v HideFirstRunExperience /t REG_DWORD /d 1 /f >nul

rem Never sleep, hibernate or blank the screen (hibernation off also turns
rem off Fast Startup, so shutting down really shuts down).
powercfg /change monitor-timeout-ac 0
powercfg /change standby-timeout-ac 0
powercfg /change hibernate-timeout-ac 0
powercfg /hibernate off

rem No Windows Update restarts while someone is signed in.
reg add HKLM\SOFTWARE\Policies\Microsoft\Windows\WindowsUpdate\AU /v NoAutoRebootWithLoggedOnUsers /t REG_DWORD /d 1 /f >nul

echo done
