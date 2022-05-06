@Echo Off
:: Open Powershell with Administrator privilege
%1 mshta vbscript:CreateObject("Shell.Application").ShellExecute("powershell.exe","/c %~s0 ::","","runas",1)(window.close)&&exit
cd /d "%~dp0"

:: Set start time
for /f "tokens=2 delims==" %%a in ('wmic OS Get localdatetime /value') do set "dt=%%a"
set "YYYY=%dt:~0,4%" & set "MM=%dt:~4,2%" & set "DD=%dt:~6,2%" & set "HH=%dt:~8,2%" & set "Min=%dt:~10,2%" & set "Sec=%dt:~12,2%"
set "start_time=%YYYY%-%MM%-%DD%_%HH%-%Min%-%Sec%"

:: Print screen message to users
echo:  
echo        #######################################################################
echo        #                                                                     #
echo        #                    Welcome to Skywire [Windows]                     #
echo        #                                                                     #
echo        #     - You have access to Hyperviro UI by http://127.0.0.1:8000      #
echo        #     - All logs available in C:\Program Files\Skywire\local\logs     #
echo        #     - You can terminate skywire by Ctrl+C command                   #
echo        #                                                                     #
echo        #######################################################################
echo:

:: Create logs folder if not exist. Run just in first time after install
if not exist "local\logs\" (
	mkdir "local\logs"
)

:: Move vpn-client.exe to its path
if exist vpn-client.exe (
    if not exist "apps\" (
        mkdir apps
    )
    move /Y vpn-client.exe apps
)

:: Move wintun.dll to system32 path
if exist "wintun.dll" (
    move /Y wintun.dll "C:\Windows\System32"
)

:: Move existed config file in user home to here
if exist "%HOMEPATH%\skywire-config.json" (
	move /Y "%HOMEPATH%\skywire-config.json" .
)

:: Genereate new config file if not exist
if not exist "skywire-config.json" (
    skywire-cli config gen -birp --os windows --disableapps skychat,skysocks,skysocks-client,vpn-server
)

:: After update and install new version of skywire, regenerate config file for update values and version
if exist "new.update" (
    skywire-cli config gen -birpx --os windows --disableapps skychat,skysocks,skysocks-client,vpn-server
    del new.update
)

:: Open UI
start "" http://127.0.0.1:8000

:: Run skywire
skywire-visor.exe -c "skywire-config.json" >> local\logs\skywire_%start_time%.log
