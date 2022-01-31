echo "Building Systray Windows Binaries..."
if (Test-Path ".\skywire.msi") { Remove-Item ".\skywire.msi" -Recurse -Force}
make build-systray-windows > $null
echo "Build Complete!"
echo "Setting go-msi configuration..."
cd .\scripts\win_installer
if (Test-Path ".\build") { Remove-Item ".\build" -Recurse -Force}
mkdir -p ".\build\amd64\apps" > $null
mv ..\..\skywire-visor.exe .\build\amd64\skywire-visor.exe
mv ..\..\skywire-cli.exe .\build\amd64\skywire-cli.exe
mv ..\..\apps\vpn-client .\build\amd64\apps\vpn-client.exe
rm ..\..\setup-node.exe
rm -r -fo ..\..\apps
cp skywire.bat .\build\amd64\skywire.bat
echo "Configuration complete!"
echo "Building msi..."
go-msi make --msi skywire.msi --version 0.5.1 --arch amd64
mv skywire.msi ../../skywire.msi
Remove-Item ".\build" -Recurse -Force
cd ../../
echo "Build Complete!"
