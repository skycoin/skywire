#!/bin/bash

sudo apt install devscripts binutils-i686-linux-gnu binutils-aarch64-linux-gnu binutils-arm-linux-gnueabi \
    dh-systemd build-essential crossbuild-essential-armhf gpg dpkg-sig

sudo chmod +x ./scripts/deb_installer/package_deb.sh

errormessage=$( sudo ln -s arm-linux-gnueabi-strip /usr/bin/arm-linux-gnu-strip 2>&1)
if ! [[ $errormessage =~ 'File exists' ]]; then
    echo $errormessage
fi
