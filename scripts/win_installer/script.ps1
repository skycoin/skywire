Write-Output ".:: Create MSI Installer Package ::.  "

Write-Output "    1. Preparing Stage..."
if (Test-Path ".\archive.zip") { Remove-Item ".\archive.zip" -Recurse -Force}
if (Test-Path ".\archive") { Remove-Item ".\archive" -Recurse -Force}
if (Test-Path ".\scripts\win_installer\build") { Remove-Item ".\scripts\win_installer\build" -Recurse -Force}
if (Test-Path ".\scripts\win_installer\wintun.zip") { Remove-Item ".\scripts\win_installer\wintun.zip" -Recurse -Force}
if (Test-Path ".\scripts\win_installer\wintun") { Remove-Item ".\scripts\win_installer\wintun" -Recurse -Force}
if (Test-Path ".\scripts\win_installer\skywire.msi") { Remove-Item ".\scripts\win_installer\skywire.msi" -Recurse -Force}

Write-Output "    2. Fetching Windows Archive from GitHub..."
$version = $args[0]
if ($version -eq "latest") {
    $url = 'https://github.com/skycoin/skywire/releases/latest'
    $request = [System.Net.WebRequest]::Create($url)
    $response = $request.GetResponse()
    $realTagUrl = $response.ResponseUri.OriginalString
    $version = $realTagUrl.split('/')[-1].Trim('v')
    $fileName = "skywire-systray-v$version-windows-amd64"
    $downloadURL = "https://github.com/skycoin/skywire/releases/download/v$version/$filename.zip"
    Invoke-WebRequest $downloadURL -o archive.zip
} else {
    $fileName = "skywire-systray-$version-windows-amd64"
    $downloadURL = "https://github.com/skycoin/skywire/releases/download/$version/$filename.zip"
    Invoke-WebRequest $downloadURL -o archive.zip
}

Write-Output "    3. Extracing Downloaded Archive File..."
Expand-Archive -Path archive.zip

Write-Output "    4. Preparing Environment for Go-MSI..."
Set-Location .\scripts\win_installer
mkdir -p ".\build\amd64\apps" > $null
Move-Item ..\..\archive\skywire-visor.exe .\build\amd64\skywire-visor.exe
Move-Item ..\..\archive\skywire-cli.exe .\build\amd64\skywire-cli.exe
Move-Item ..\..\archive\apps\vpn-client.exe .\build\amd64\apps\vpn-client.exe
Copy-Item ..\..\archive\dmsghttp-config.json .\build\amd64\dmsghttp-config.json
Copy-Item skywire.bat .\build\amd64\skywire.bat
New-Item new.update  > $null
Move-Item new.update .\build\amd64\new.update
Invoke-WebRequest "https://www.wintun.net/builds/wintun-0.14.1.zip" -o wintun.zip
Expand-Archive wintun.zip
Copy-Item .\wintun\wintun\bin\amd64\wintun.dll .\build\amd64\wintun.dll

Write-Output "    4. Building MSI Installer..."
go-msi make --msi skywire.msi --version 1.0.0 --arch amd64  > $null
Move-Item skywire.msi ../../$fileName.msi -Force

Write-Output "       =====> BINGO! Build Completed!"


Write-Output "    5. Cleaning Stage..."
Set-Location ../../
if (Test-Path ".\archive.zip") { Remove-Item ".\archive.zip" -Recurse -Force}
if (Test-Path ".\archive") { Remove-Item ".\archive" -Recurse -Force}
if (Test-Path ".\scripts\win_installer\build") { Remove-Item ".\scripts\win_installer\build" -Recurse -Force}
if (Test-Path ".\scripts\win_installer\wintun.zip") { Remove-Item ".\scripts\win_installer\wintun.zip" -Recurse -Force}
if (Test-Path ".\scripts\win_installer\wintun") { Remove-Item ".\scripts\win_installer\wintun" -Recurse -Force}
if (Test-Path ".\scripts\win_installer\skywire.msi") { Remove-Item ".\scripts\win_installer\skywire.msi" -Recurse -Force}

Write-Output "`nYour Installer Ready! Have a Nice Day! Goodbye!"
