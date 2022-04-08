@Echo Off
%1 mshta vbscript:CreateObject("Shell.Application").ShellExecute("cmd.exe","/c %~s0 ::","","runas",1)(window.close)&&exit
cd /d "%~dp0"
if exist vpn-client.exe (
    if not exist "apps\" (
        mkdir apps
    )
    move /Y vpn-client.exe apps
)
if exist "wintun.dll" (
    move /Y wintun.dll "C:\Windows\System32"
)
if exist "%HOMEPATH%\skywire-config.json" (
	move /Y "%HOMEPATH%\skywire-config.json" .
)
if not exist "skywire-config.json" (
skywire-cli config gen -birp --os windows --disableapps skychat,skysocks,skysocks-client,vpn-server
)
start "" http://127.0.0.1:8000
skywire-visor.exe -c "skywire-config.json"
