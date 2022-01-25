@Echo Off
start "" http://127.0.0.1:8000
if exist "%HOMEPATH%\skywire-config.json" (
skywire-visor.exe -c "%HOMEPATH%\skywire-config.json"
) else (
skywire-cli config gen -iro "%HOMEPATH%\skywire-config.json" --os windows --disable-apps skychat,skysocks,skysocks-client,vpn-servergi
skywire-visor.exe -c "%HOMEPATH%\skywire-config.json"
)
