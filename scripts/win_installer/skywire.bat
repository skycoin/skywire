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

:: Config generation. The `-b` flag used to map to a removed
:: BESTPROTO knob; passing it to current binaries makes cobra reject
:: the entire command line ("unknown shorthand flag: 'b'") and the
:: gen never ran. With the gen short-circuited, the visor's own
:: bootstrap fell through to fresh-key generation on every schema
:: change, which is what operators were reporting as "my SK changed
:: on update". Dropping `-b` lets gen actually run; `-r` then
:: preserves the SK from the existing config (mirrors the deb/arch
:: post_install logic).
::
:: First install (no skywire-config.json yet) — generate fresh.
if not exist "skywire-config.json" (
    skywire cli config gen -irpw --disableapps vpn-server -S services-config.json -D dmsghttp-config.json --loglvl info >nul 2>&1
)

:: Upgrade marker (new.update shipped in MSI) — regenerate with `-r`.
:: The regen path reads the existing skywire-config.json, extracts
:: the SK, then writes a fresh config carrying that same SK. `-x`
:: also preserves the existing hypervisor PK list. SK preserved
:: across every MSI update.
if exist "new.update" (
    skywire cli config gen -irpwx --disableapps vpn-server -S services-config.json -D dmsghttp-config.json --loglvl info >nul 2>&1
    del new.update >nul 2>&1
)

:: Opening UI
start "" http://127.0.0.1:8000

:: Running Skywire
skywire.exe visor -c "skywire-config.json" --systray >> local\logs\skywire_%start_time%.log
