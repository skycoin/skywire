@Echo Off
:: Opening Powershell with Administrator privilege
%1 mshta vbscript:CreateObject("Shell.Application").ShellExecute("powershell.exe","/c %~s0 ::","","runas",1)(window.close)&&exit
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

:: Generating new config file if not exist
if not exist "skywire-config.json" (
    skywire-cli config gen -birpw --os windows --disableapps skychat,skysocks,skysocks-client,vpn-server >nul 2>&1
)

:: Regenerating config file after update and install new version of Skywire
if exist "new.update" (
    skywire-cli config gen -birpwx --os windows --disableapps skychat,skysocks,skysocks-client,vpn-server >nul 2>&1
    del new.update >nul 2>&1
)

:: Opening UI
start "" http://127.0.0.1:8000

:: Running Skywire
skywire-visor.exe -c "skywire-config.json" >> local\logs\skywire_%start_time%.log
