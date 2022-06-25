## Windows installer

### Requirments
- Windows Machine
- Download and Install **Wix** from [here](https://github.com/wixtoolset/wix3/releases/tag/wix3112rtm).
  - Need to add `C:\Program Files (x86)\WiX Toolset v3.11\bin` to path.
- We need to install **.Net Framework 3.5 SP1**, you can download and install from [here](https://dotnet.microsoft.com/en-us/download/dotnet-framework/thank-you/net35-sp1-web-installer).

### Build
You can build **skywire.msi** by
```
make win-installer CUSTOM_VERSION=xxx
```
or
```
make win-installer latest
```
__Note:__ _Make sure cloned this repo in `C:\` (or other partition that Windows installed)._

### Install
Double click the created installer to install skywire.

### Run
To run skywire open a terminal or cmd window and run
```
skywire
```
or open `skywire` shortcut on start menu or desktop.
