#!/bin/bash

print_usage()
{
   # Display Help
   echo
   echo "Usage: bash package_deb.sh [-h]"
   echo "You need to provide Version: version number of the binary (eg. 0.5.0)."
   echo "You need to provide Author Email: email of the author you want to sign the binary with (eg. someemail@email)."
   echo "You need to provide Author Name: name of the author you want to sign the binary with (eg. 'Some Name')."
   echo
}

# Has to be run from Debian based distro
CURRENTOS="$(cat /etc/os-release | grep ID_LIKE)"
if [[ "$CURRENTOS" != "ID_LIKE=debian" ]]; then
  echo "Can only be run from Debian based distro"
  exit 1
fi

while getopts h flag
do
    case "${flag}" in
        h)
			print_usage
			exit 0
		;;
    esac
done

function read_version() {
	read -p 'Version : ' VER
	if [ -z "$VER" ]
	then
		echo "Verson requires to be non-empty."
		read_version
	fi
}

function read_email() {
	read -p 'Author Email : ' AUTHOREMAIL
	if [ -z "$AUTHOREMAIL" ]
	then
		echo "Author Email requires to be non-empty."
		read_email
	fi
}

function read_name() {
	read -p 'Author Name : ' AUTHORNAME
	if [ -z "$AUTHORNAME" ]
	then
		echo "Author Name requires to be non-empty."
		read_name
	fi
}

read_version
read_email
read_name

REPONAME=skywire
BUILDTAG=debian

function create_control_file {
	if [ -z "$1" ]
	then
		exit
	fi

	ARCH=$1
	cat <<EOF >./debian/control
Source: $REPONAME
Maintainer: $AUTHORNAME <$AUTHOREMAIL>
Standards-Version: $VER
Section: base
Build-Depends: dh-systemd (>= 1.5)

Package: $REPONAME
Priority: optional
Architecture: $ARCH
Description: Skywire applications to participate in skywire network
EOF
}

function pack_deb {
	if [ -z $1 ]
	then
		exit
	fi

	if [ -z $2 ]
	then
		exit
	fi

	ARCH=$1
	GOARCH=$2

	GOOS=linux GOARCH="$GOARCH" make build BUILDTAG=$BUILDTAG

	rm -rf packages
	mkdir packages
	cd ./packages

	mkdir "./$REPONAME-$VER"
	cp ../skywire-visor "./$REPONAME-$VER/"
	cp ../skywire-cli "./$REPONAME-$VER/"
	mkdir "./$REPONAME-$VER/apps"
	cp ../apps/skychat "./$REPONAME-$VER/apps/"
	cp ../apps/skysocks "./$REPONAME-$VER/apps/"
	cp ../apps/skysocks-client "./$REPONAME-$VER/apps/"
	cp ../apps/vpn-client "./$REPONAME-$VER/apps/"
	cp ../apps/vpn-server "./$REPONAME-$VER/apps/"

	cd "./$REPONAME-$VER"

	# this may seem unnecessary, but for some reason the lack of distclean target
	# prevents debuild from passing DESTDIR correctly in the makefile.
	cp ../../scripts/deb_installer/Makefile ./Makefile
	mkdir ./debian

	create_control_file "$ARCH"

	# this will bring up the editor
	DEBEMAIL=$AUTHOREMAIL DEBFULLNAME=$AUTHORNAME dch --create -d --distribution stable
	
	cp ../../scripts/deb_installer/rules ./debian/rules

	echo "Copyright $(date +%Y), Skycoin." >> ./debian/copyright

	echo "10" >> ./debian/compat

	cp ../../scripts/deb_installer/deb.prerm "./debian/${REPONAME}.prerm"

	chmod 0555 "./debian/${REPONAME}.prerm"

	cp ../../scripts/deb_installer/deb.preinst "./debian/${REPONAME}.preinst"

	chmod 0555 "./debian/${REPONAME}.preinst"

	cp ../../scripts/deb_installer/deb.postrm "./debian/${REPONAME}.postrm"

	chmod 0555 "./debian/${REPONAME}.postrm"

	cp ../../scripts/deb_installer/deb.postinst "./debian/${REPONAME}.postinst"

	chmod 0555 "./debian/${REPONAME}.postinst"

	cp ../../scripts/deb_installer/skywire.service ./debian/skywire.service

	DEBEMAIL=$AUTHOREMAIL DEBFULLNAME=$AUTHORNAME debuild -a"$ARCH" -us -uc

	cd ..
	echo "$PWD"
	ls -la
	mv "./${REPONAME}_${VER}-1_${ARCH}.deb" ../deb/
	cd ..
	rm -rf ./packages
	rm -rf ./debian
}

set -euo pipefail

sudo dpkg --add-architecture armhf

rm -rf ./deb
mkdir ./deb

pack_deb amd64 amd64
pack_deb i386 386
pack_deb arm64 arm64
pack_deb arm arm
pack_deb armhf arm

cd ../..
rm -rf "packaging"
