## Mac Installer

### Sort of Questions
- Q: *What is the equivalent of the systemd service used for controlling the running process?*
  
  A:  No systemd service there. User just should run app from menu or desktop shortcut.
- Q: *How do you manually start and stop skywire via this framework?*
  
  A:  Running by shortcut on desktop or menu.
- Q: *How do you enable or disable skywire starting at boot?*
  
  A:  Not available by UI or other things. Should add it manually by user to startup items.
- Q: *What is the default config file path?*
  
  A:  `/Users/{user}/Library/Application Support/Skywire/skywire-config.json`
- Q: *What is included in the packaging besides skywire? (i.e. scripts, services, batch files, etc.)*
  
  A:  - a bash script called **Skywire** that include a command to run skywire-visor on config. (Line 82 of create_installer.sh)
- Q: *Where are the sources for the build of the installer or package?*
  
  A:  `scripts\mac_installer`
- Q: *What are the dependencies required to build either of these? mac package*
  
  A:  nothing
