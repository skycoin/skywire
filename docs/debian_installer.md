# Debian

## Installation

### Developer

Prequisites:

- Debian Host
- devscripts
- binutils-i686-linux-gnu
- binutils-aarch64-linux-gnu
- binutils-arm-linux-gnueabi
- dh-systemd
- build-essential
- crossbuild-essential-armhf
- gpg
- dpkg-sig


1. In order to create the Skywire debian packages, make sure you are running a debian based distro and install the following packages 

```
$ sudo apt install devscripts binutils-i686-linux-gnu binutils-aarch64-linux-gnu binutils-arm-linux-gnueabi dh-systemd build-essential crossbuild-essential-armhf gpg dpkg-sig
```

2. Make sure you make the packaging script exectuable:
```bash
$ sudo chmod +x ./scripts/debian_installer/package_deb.sh
```

3. Symlink the binary:
```
$ sudo ln -s arm-linux-gnueabi-strip /usr/bin/arm-linux-gnu-strip
```

4. To sign the debian packages, add both the public and secret key if not already.
Follow [this guide](https://www.debuntu.org/how-to-importexport-gpg-key-pair/) for more info.
 
```bash
$ gpg --import ~/mygpgkey_pub.gpg
$ gpg --allow-secret-key-import --import ~/mygpgkey_sec.gpg
```

5. Check if it is added correctly:
```bash
$ gpg --list-keys
```

6. Run the packaging script. Make sure the eamil and full_name is the same as the key in `gpg --list-keys`:
(NOTE: the email should be the same as the email of the key you are sigining with)
```bash
$ ./scripts/debian_installer/package_deb.sh 0.4.0 some@mail.ru 'full name'
```

The first argument is a package version. The debian package should be created after a release has been made in `github.com/skycoin/skywire` and the version should follow the tag defined in the `skywire` repo. 

During its work script will create packages for the following architectures:
- amd64
- i386
- arm
- arm64
- armhf

For each architecture a changelog file will be created for a package and will be opened with the editor. Apply needed changes and save it.

The script will complain that there's no original code tarball. Ignore the warning by pressing `y`. 

Once the script finishes, you'll see a `deb` directory in you current folder. This is where the finished packages are. Put these to `/var/www/repos/apt/debian` on the `apt server` and remove the old packages there. 

7. To sign the packages use:<br>
(NOTE: the email should be the same as the one used  in 6. as well as the email of the key you are sigining with)
```bash
$ ./scripts/debian_installer/sign_deb.sh some@mail.ru
```

This will sign all packages.

8. To verify the signature of a single package use:
```bash
$ dpkg-sig --verify ./deb/skywire_0.4.0-1_amd64.deb
Processing ./deb/skywire_0.4.0-1_amd64.deb...
GOODSIG _gpgbuilder 9A86A72D257E9EEAD3CE6ADDCC2026E6F21CEDA7 1620395902
```

- If the package isn't signed, then the output will be `NOSIGN`.
- If there is some error, it will be `BADSIG`.
- If the package is signed, then the output will be `GOODSIG`. Followed by who signed the package `_gpgbuilder` 
and then the keyid of the key that the package is signed with `9A86A72D257E9EEAD3CE6ADDCC2026E6F21CEDA7`
and lastly the epoch time of when it was signed `1620395902`.
To verify that the correct key was used for the signing, compare the `keyid` with the correct `keyid` from 
`gpg --list-keys`.

9. To remove old packages from the repo, cd to `/var/www/repos/apt/debian` and use:
```bash
$ reprepro remove stretch skywire
```

This should be repeated for all needed debian releases.

10. To add new packages to the repo, from the `/var/www/repos/apt/debian` use:
```bash
$ reprepro includedeb jessie ./skywire_0.4.0-1_amd64.deb
```

This should be repeated for all needed debian releases and built packages.

### End User

1. Import the public key
```bash
$ wget -O - http://subdomain.skycoin.com/public-gpg.key | sudo apt-key add - 
```

2. Add repo to sources.list

```bash
$ deb http://subdomain.skycoin.com/ skywire main
```

3. Run
```bash
$ apt-get update
```
