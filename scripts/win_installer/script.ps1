
$ErrorActionPreference = "Stop"
$version = $args[0]
function CleanStage
{
    if (Test-Path ".\archive.zip") { Remove-Item ".\archive.zip" -Recurse -Force}
    if (Test-Path ".\archive") { Remove-Item ".\archive" -Recurse -Force}
    if (Test-Path ".\scripts\win_installer\build") { Remove-Item ".\scripts\win_installer\build" -Recurse -Force}
    if (Test-Path ".\scripts\win_installer\wintun.zip") { Remove-Item ".\scripts\win_installer\wintun.zip" -Recurse -Force}
    if (Test-Path ".\scripts\win_installer\wintun") { Remove-Item ".\scripts\win_installer\wintun" -Recurse -Force}
    if (Test-Path ".\scripts\win_installer\wix.zip") { Remove-Item ".\scripts\win_installer\wix.zip" -Recurse -Force}
    if (Test-Path ".\scripts\win_installer\wix") { Remove-Item ".\scripts\win_installer\wix" -Recurse -Force}
    if (Test-Path ".\scripts\win_installer\UI.wixobj") { Remove-Item ".\scripts\win_installer\UI.wixobj" -Recurse -Force}
    if (Test-Path ".\scripts\win_installer\Product.wixobj") { Remove-Item ".\scripts\win_installer\Product.wixobj" -Recurse -Force}
    if (Test-Path ".\scripts\win_installer\skywire.msi") { Remove-Item ".\scripts\win_installer\skywire.msi" -Recurse -Force}
}

function InstallWix
{
    Set-Location .\scripts\win_installer
    Invoke-WebRequest "https://github.com/wixtoolset/wix3/releases/download/wix3112rtm/wix311-binaries.zip" -o wix.zip
    Expand-Archive wix.zip
    Set-Location ../../
}

function BuildInstaller($arch)
{
    if ($arch -eq "386") {
        $wintun_arch="x86"
        $arch_title="386  "
        $wix_arch="x86"
    } else {
        $wintun_arch="amd64"
        $arch_title="amd64"
        $wix_arch="x64"
    }

    Write-Output "#                                                        #"
    Write-Output "#    => Create Installer for $arch_title                       #"
    Write-Output "#       0. Preparing Stage...                            #"
    CleanStage
    Write-Output "#       1. Installing Wix...                             #"
    InstallWix
    Write-Output "#       2. Fetching Archive from GitHub...               #"
    if ($version -eq "latest") {
        $url = 'https://github.com/mrpalide/skywire/releases/latest'
        $request = [System.Net.WebRequest]::Create($url)
        $response = $request.GetResponse()
        $realTagUrl = $response.ResponseUri.OriginalString
        $version = $realTagUrl.split('/')[-1].Trim('v')
        $fileName = "skywire-v$version-windows-$arch"
        $msiName = "skywire-installer-v$version-windows-$arch"
        $downloadURL = "https://github.com/mrpalide/skywire/releases/download/v$version/$filename.zip"
        Invoke-WebRequest $downloadURL -o archive.zip -ErrorAction Stop
    } else {
        $fileName = "skywire-$version-windows-$arch"
        $msiName = "skywire-installer-$version-windows-$arch"
        $downloadURL = "https://github.com/mrpalide/skywire/releases/download/$version/$filename.zip"
        Invoke-WebRequest $downloadURL -o archive.zip
    }

    Write-Output "#       3. Extracing Downloaded Archive File...          #"
    Expand-Archive -Path archive.zip

    Write-Output "#       4. Preparing Environment for Wix...              #"
    Set-Location .\scripts\win_installer
    mkdir -p ".\build\apps" > $null
    Move-Item ..\..\archive\skywire-visor.exe .\build\skywire-visor.exe
    Move-Item ..\..\archive\skywire-cli.exe .\build\skywire-cli.exe
    Move-Item ..\..\archive\apps\vpn-client.exe .\build\apps\vpn-client.exe
    Copy-Item ..\..\archive\dmsghttp-config.json .\build\dmsghttp-config.json
    Copy-Item ..\..\archive\skycoin.asc .\build\skycoin.asc
    Copy-Item skywire.bat .\build\skywire.bat
    New-Item new.update  > $null
    Move-Item new.update .\build\new.update
    Invoke-WebRequest "https://www.wintun.net/builds/wintun-0.14.1.zip" -o wintun.zip
    Expand-Archive wintun.zip
    Copy-Item .\wintun\wintun\bin\$wintun_arch\wintun.dll .\build\wintun.dll

    Write-Output "#       5. Building MSI Installer...                     #"
    .\wix\candle.exe UI.wxs Product.wxs -arch $wix_arch > $null
    .\wix\light.exe -ext WixUIExtension -ext WixUtilExtension -sacl -spdb -out skywire.msi UI.wixobj Product.wixobj  > $null
    Move-Item skywire.msi ../../$msiName.msi -Force

    Write-Output "#          ==> Build Completed for $arch_title!                #"
    
    Write-Output "#       6. Cleaning Stage...                             #"
    Set-Location ../../
    CleanStage

    Write-Output "#       7. Done!                                         #"
} 

Write-Output "`n##########################################################"
Write-Output "#                                                        #"
Write-Output "#        .:::: Create MSI Installer Package ::::.        #"
BuildInstaller("amd64")
BuildInstaller("386")
Write-Output "#                                                        #"
Write-Output "##########################################################`n"
