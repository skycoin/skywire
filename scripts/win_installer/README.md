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
__Note:__ _Make sure cloned this repo in `C:\`(or other partition that Windows installed) and run in a terminal without administrator privileges._

### Install
Double click the created installer to install skywire.

### Run
To run skywire open a terminal or cmd window and run
```
skywire
```
or open `skywire` shortcut on start menu or desktop.

### Sort of Questions
- Q: *What is the equivalent of the systemd service used for controlling the running process?*
  
  A:  No systemd service there. User just should run app from menu or desktop shortcut.
- Q: *How do you manually start and stop skywire via this framework?*
  
  A:  Running by shortcut on desktop or menu.
- Q: *How do you enable or disable skywire starting at boot?*
  
  A:  Not available by UI or other things. Should add it manually by user to startup items.
- Q: *What is the default config file path?*
  
  A:  `C:\Program Files\Skywire\skywire-config.json`
- Q: *What is included in the packaging besides skywire? (i.e. scripts, services, batch files, etc.)*
  
  A:  - a `.bat` file that use for running skywire
      - a `new.update` file for checking update then regenerate config file for update version and etc
- Q: *Where are the sources for the build of the installer or package?*
  
  A:  `scripts\win_installer`
- Q: *What are the dependencies required to build either of these? .msi*
  
  A:  `wix` and `wintun` , but both of them downloaded during building `.msi` installer
