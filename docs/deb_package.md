# Debian Package

### Developer

Prequisites:

- Archlinux host or chroot
- [`yay`](https://github.com/Jguer/yay)
- `dpkg`

Archlinux packaging tools included with the operating system (`makepkg`) and the [skywire AUR cross-compiling .deb PKGBUILD](https://aur.archlinux.org/cgit/aur.git/tree/PKGBUILD?h=skywire-bin) are used to create the skywire .deb packages. `pacman`, the archlinux package manager, is used to handle dependencies. `yay` acts as a wrapper for `pacman` and `makepkg`, which shaves a few commands off this process.

Three types of build are currently possible:
* a build which sources the binary release of skywire
* a build from source using a versioned release archive
* a build from cloned github sources using a specific branch or commit

#### Archlinux Chroot Setup

The archlinux chroot can be set up in one of several ways which are detaled in the archwiki page on [installing from existing linux](https://wiki.archlinux.org/title/Install_Arch_Linux_from_existing_Linux)

If there is no constraint that the host must be debian-based; it is preferable to use a native archlinux installation (such as [EndeavourOS](https://endeavouros.com/latest-release/))

Once the chroot of archlinux has been attained, a few steps are needed to set it up. If using endeavourOS, these steps have already taken place during the OS installation process.

```
pacman-key --init
pacman-key --populate
pacman -Syy && pacman -Syu sudo git base-devel
```

A user must be created in the archlinux chroot and granted sudo privelages. This process is the same on .deb based distros as on archlinux.

Once the privileged user has been created, switch to that user

```
su - user
```

#### Install `yay`

`yay` is included with endeavourOS; skip this step if using an endeavourOS host

manually install yay:

```
mkdir -p ~/.cache/yay && cd ~/.cache/yay
git clone https://aur.archlinux.org/yay-git
cd yay-git
makepkg -scif
```

#### Install `dpkg` with `yay`

install `dpkg`from the AUR with `yay`
```
yay -S dpkg
```

#### Creating the skywire-bin debian package

install skywire-bin with `yay` (which will fetch the debian PKGBUILD included in the skywire AUR repository)
```
yay -S skywire-bin
```

skywire-bin may then be uninstalled from the arch host or chroot
```
yay -R skywire-bin
```

create the skywire-bin debian packages with `makepkg`
```
cd ~/.cache/yay/skywire-bin
makepkg -p cc.deb.PKGBUILD
```

#### Creating the skywire debian package (versioned release archive)

install skywire with `yay` (which will fetch the debian PKGBUILD included in the skywire AUR repository)
```
yay -S skywire
```

skywire-bin may then be uninstalled from the arch host or chroot
```
yay -R skywire
```

create the skywire debian packages with `makepkg`
```
cd ~/.cache/yay/skywire
makepkg -p cc.deb.PKGBUILD
```

#### Updating PKGBUILDs in the AUR

One must belisted as maintainer of the AUR repository in question to update the PKGBUILD and included files

SSH Clone the AUR repository:
```
mkdir -p ~/aur && cd ~/aur
git clone ssh://aur@aur.archlinux.org/skywire-bin
cd skywire-bin
```

typically, the archlinux package is updated first and tested on the archlinux host. The systemd services will not work in the chroot; so it is preferable to use an archlinux host.

Open the PKGBUILD in an editor, extract the skywire-scripts archive and preform edits. Save the files and update the scripts archive.

update the checksums (requires pacman-contrib)

```
yay -S pacman-contrib
updpkgsums
```

create and test the package
```
makepkg -if
```

create the updated .SRCINFO metadata file for the AUR
```
makepkg --printsrcinfo > .SRCINFO
```

add the PKGBUILD, .SRCINFO, and scripts archive (if its contents have been edited) and push these changes to the AUR
```
git add-f PKGBUILD .SRCINFO skywire-scripts.tar.gz
git commit -m "update to version 0.5.0, update skywire-autoconfig script"
git push
```

After the PKGBUILD has been updated, open the deb.PKGBUILD, cc.deb.PKGBUILD, and skywire-deb-scripts archive for editing.

Preform the same changes as were preformed for the PKGBUILD. Save the files, update the checksums
```
updpkgsums cc.deb.PKGBUILD deb.PKGBUILD
```

create the .deb packages and test them on a .deb host
```
makepkg -p cc.deb.PKGBUILD
```

Push the changes to the AUR
```
git add -f cc.deb.PKGBUILD deb.PKGBUILD skywire-deb-scripts.tar.gz
git commit -m "update to version 0.5.0, update skywire-autoconfig script"
git push
```

### Updating the APT repo with new packages

Once the command finishes, you'll see `.deb` packages in you current folder. Move these to `/var/www/repos/apt/debian` on the `apt server` and remove the old packages there.

To remove old packages from the repo, cd to `/var/www/repos/apt/debian` and use:
```bash
$ reprepro remove stretch skywire
```

This should be repeated for all needed debian releases.

To add new packages to the repo, use:
```bash
$ reprepro --basedir /var/www/repos/apt/debian includedeb jessie skywire_0.5.0-1_*.deb
```

This should be repeated for all needed debian releases and built packages.

To install the package with dpkg, use:
```
$ sudo dpkg -i skywire_0.5.0-1_amd64.deb
```

To install the package with apt, assuming the repository is configured in `/etc/apt/sources.list` and the key used for signing the repository has been imported, use:
```
$ sudo apt install skywire
```

To uninstall the package use:
```
$ sudo apt remove skywire
```

### End User

Import the public key
```bash
$ wget -O - http://subdomain.skycoin.com/public-gpg.key | sudo apt-key add -
```

Add repo to sources.list

```bash
$ deb http://subdomain.skycoin.com/ skywire main
```

Run
```bash
$ apt update
```

To uninstall use:
```
$ sudo apt remove skywire
```
