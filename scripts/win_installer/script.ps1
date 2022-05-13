echo "Building Systray Windows Binaries..."
if (Test-Path ".\skywire.msi") { Remove-Item ".\skywire.msi" -Recurse -Force}
if (Test-Path ".\wintun.zip") { Remove-Item ".\wintun.zip" -Recurse -Force}
make build-systray-windows BUILDTAG="Windows" > $null
echo "Build Complete!"
echo "Setting go-msi configuration..."
cd .\scripts\win_installer
if (Test-Path ".\build") { Remove-Item ".\build" -Recurse -Force}
mkdir -p ".\build\amd64\apps" > $null
mv ..\..\skywire-visor.exe .\build\amd64\skywire-visor.exe
mv ..\..\skywire-cli.exe .\build\amd64\skywire-cli.exe
mv ..\..\apps\vpn-client.exe .\build\amd64\apps\vpn-client.exe
cp ..\..\dmsghttp-config.json .\build\amd64\dmsghttp-config.json
rm ..\..\setup-node.exe
rm -r -fo ..\..\apps
cp skywire.bat .\build\amd64\skywire.bat
ni new.update
mv new.update .\build\amd64\new.update
curl "https://www.wintun.net/builds/wintun-0.14.1.zip" -o wintun.zip
tar -xf wintun.zip
cp .\wintun\bin\amd64\wintun.dll .\build\amd64\wintun.dll
rm -r -fo wintun
rm wintun.zip
echo "Configuration complete!"
echo "Building msi..."
go-msi make --msi skywire.msi --version 1.0.0 --arch amd64
mv skywire.msi ../../skywire.msi
Remove-Item ".\build" -Recurse -Force
cd ../../
echo "Build Complete!"
