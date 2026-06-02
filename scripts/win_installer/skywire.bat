@Echo Off
:: Opening Powershell with Administrator privilege
net session >nul 2>&1
if %errorlevel% NEQ 0 (
    echo Requesting administrator privileges...
    powershell -Command "Start-Process cmd -Argument '/c \"%~f0\"' -Verb RunAs"
    exit
)

cd /d "%~dp0"

:: Setting start time
for /f "tokens=2 delims==" %%a in ('wmic OS Get localdatetime /value') do set "dt=%%a"
set "YYYY=%dt:~0,4%" & set "MM=%dt:~4,2%" & set "DD=%dt:~6,2%" & set "HH=%dt:~8,2%" & set "Min=%dt:~10,2%" & set "Sec=%dt:~12,2%"
set "start_time=%YYYY%-%MM%-%DD%_%HH%-%Min%-%Sec%"

:: Printing screen message to users
echo:  
echo        ########################################################################
echo        #                                                                      #
echo        #                     Welcome to Skywire [Windows]                     #
echo        #                                                                      #
echo        #    - You have access to Hypervisor UI by http://127.0.0.1:8000       #
echo        #    - All logs be available in C:\Program Files\Skywire\local\logs    #
echo        #    - You can terminate Skywire by Ctrl+C command.                    #
echo        #                                                                      #
echo        ########################################################################
echo:

:: Creating logs folder if not exist [Run just in first time after installing]
if not exist "local\logs\" (
	mkdir "local\logs" >nul 2>&1
)

:: Moving wintun.dll to system32 path
if exist "wintun.dll" (
    move /Y wintun.dll "C:\Windows\System32" >nul 2>&1
)

:: Moving existed config file in user home to installation path
if exist "%HOMEPATH%\skywire-config.json" (
	move /Y "%HOMEPATH%\skywire-config.json" . >nul 2>&1
)

:: Config setup — env file (%ProgramData%\Skywire\skywire.conf) + visor
:: config (skywire-config.json). Shared with the MSI install-time
:: CustomAction (Product.wxs) so install and launch run identical,
:: idempotent generate-if-missing logic. The called script also sets
:: SKYENV, which is inherited by the visor launch below.
:: See skywire-autoconfig.bat.
call "%~dp0skywire-autoconfig.bat"

:: Opening UI
start "" http://127.0.0.1:8000

:: Running Skywire
skywire.exe visor -c "skywire-config.json" --systray >> local\logs\skywire_%start_time%.log
