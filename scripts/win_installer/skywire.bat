@Echo Off
%1 mshta vbscript:CreateObject("Shell.Application").ShellExecute("cmd.exe","/c %~s0 ::","","runas",1)(window.close)&&exit
cd /d "%~dp0"
if exist "wintun.dll" (
    move /Y wintun.dll "C:\Windows\System32"
)
if not exist "%HOMEPATH%\skywire-config.json" (
skywire-cli config gen -biro "%HOMEPATH%\skywire-config.json" --os windows --disable-apps skychat,skysocks,skysocks-client,vpn-server
)
start "" http://127.0.0.1:8000
skywire-visor.exe -c "%HOMEPATH%\skywire-config.json"
