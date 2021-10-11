# Building Systray

To build `skywire-visor` with systray feature enabled, you have to had these installed on your system:

### Prequisites

#### Linux

- Debian / Ubuntu and its derivations

```bash
$ sudo apt install libgtk-3-dev libappindicator3-dev
```

- Fedora / RHEL and its derivations

```bash
$ sudo dnf install gtk3-devel libappindicator-gtk3-devel
```

- ArchLinux and its variants

```bash
$ sudo pacman -S libappindicator-gtk3 gtk3
```

Other distros might require the installation of said library in their own respective name.

#### Mac / Darwin

You need to have `XCode` installed.

#### Windows

- WIP

### Build

The following command will build the systray app to the root of this repo

```bash
$ make build-systray
```

### Running

#### Linux

- You need to have an icon defined in `/opt/skywire/icon.png` (WIP, provide linux installer for it)
- You need to install `gnome-shell-extension-appindicator`
- After you installed the extension, go to the tweaks section of your gnome shell settings, and
  activate `Kstatusnotifieritem / appindicator support`
- Alternatively, you can use:

```bash
$ gnome-extensions enable ubuntu-appindicators@ubuntu.com
```

- Logout, and run the application

#### Mac / Darwin

You need to have an icon defined in `/Applications/Skywire.app/Contents/Resources/tray_icon.png`

#### Windows

TBD

Then you can run it with

```bash

# Linux
$ sudo setcap 'cap_net_admin+p' ./apps/vpn-client
$ ./skywire-visor -c <YOUR_CONFIG_PATH> --systray

# MacOS

$ sudo chown root ./apps/vpn-client
$ sudo chmod 4755 ./apps/vpn-client
$ ./skywire-visor -c <YOUR_CONFIG_PATH> --systray
```
