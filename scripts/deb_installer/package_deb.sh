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

	# well, okay, this may seem unnecessary, but for some reason the lack of distclean target
	# prevents debuild from passing DESTDIR correctly. This is sick, but works this way 
	cat <<EOF >./Makefile
distclean:
	echo dummy
install:
	mkdir -p \$(DESTDIR)/opt/skywire/apps
	mkdir -p \$(DESTDIR)/usr/bin
	install -m 0755 skywire-visor \$(DESTDIR)/opt/skywire/skywire-visor
	install -m 0755 skywire-cli \$(DESTDIR)/opt/skywire/skywire-cli
	install -m 0755 apps/skychat \$(DESTDIR)/opt/skywire/apps/skychat
	install -m 0755 apps/skysocks \$(DESTDIR)/opt/skywire/apps/skysocks
	install -m 0755 apps/skysocks-client \$(DESTDIR)/opt/skywire/apps/skysocks-client
	install -m 0755 apps/vpn-server \$(DESTDIR)/opt/skywire/apps/vpn-server
	install -m 0755 apps/vpn-client \$(DESTDIR)/opt/skywire/apps/vpn-client
	ln -s /opt/skywire/skywire-visor \$(DESTDIR)/usr/bin/skywire-visor
	ln -s /opt/skywire/skywire-cli \$(DESTDIR)/usr/bin/skywire-cli

uninstall:
	rm -rf \$(DESTDIR)/usr/bin/skywire-visor
	rm -rf \$(DESTDIR)/usr/bin/skywire-cli
	rm -rf \$(DESTDIR)/opt/skywire
EOF
	mkdir ./debian

	create_control_file "$ARCH"

	# this will bring up the editor
	DEBEMAIL=$AUTHOREMAIL DEBFULLNAME=$AUTHORNAME dch --create -d --distribution stable
	
	cat <<EOF >./debian/rules
#!/usr/bin/make -f
%:
	dh \$@ --with systemd
EOF

	echo "Copyright $(date +%Y), Skycoin." >> ./debian/copyright

	echo "10" >> ./debian/compat

	cat <<EOF >./debian/${REPONAME}.prerm
#!/bin/bash

touch /opt/skywire/removing

# Automatically added by dh_systemd_start/12.1.1
if [ -d /run/systemd/system ] && [ \"\$1\" = remove ]; then
	deb-systemd-invoke stop 'skywire.service' >/dev/null || true
fi
# End automatically added section

#DEBHELPER#
EOF

	chmod 0555 "./debian/${REPONAME}.prerm"

	cat <<EOF >./debian/${REPONAME}.preinst
#!/bin/bash

if [ -f "/opt/skywire/removing" ]
then
	touch /opt/skywire/upgrading
fi

#DEBHELPER#
EOF

	chmod 0555 "./debian/${REPONAME}.preinst"

	cat <<EOF >./debian/${REPONAME}.postrm
#!/bin/bash

if [ ! -f "opt/skywire/upgrading" ]
then
	rm -rf /opt/skywire
fi

# Automatically added by dh_systemd_start/12.1.1
if [ -d /run/systemd/system ]; then
	systemctl --system daemon-reload >/dev/null || true
fi
# End automatically added section
# Automatically added by dh_systemd_enable/12.1.1
if [ \"\$1\" = \"remove\" ]; then
	if [ -x \"/usr/bin/deb-systemd-helper\" ]; then
		deb-systemd-helper mask 'skywire.service' >/dev/null || true
	fi
fi

if [ \"\$1\" = \"purge\" ]; then
	if [ -x \"/usr/bin/deb-systemd-helper\" ]; then
		deb-systemd-helper purge 'skywire.service' >/dev/null || true
		deb-systemd-helper unmask 'skywire.service' >/dev/null || true
	fi
fi
# End automatically added section

#DEBHELPER#
EOF

	chmod 0555 "./debian/${REPONAME}.postrm"

	cat <<EOF >./debian/${REPONAME}.postinst
#!/bin/bash

rm -rf /opt/skywire/upgrading
if [ -f "/opt/skywire/removing" ]
then
	rm -rf /opt/skywire/removing
else
	/opt/skywire/skywire-cli visor gen-config -o /opt/skywire/skywire-config.json
fi

setcap 'cap_net_admin+p' /opt/skywire/apps/vpn-client

# Automatically added by dh_systemd_enable/12.1.1
if [ \"\$1\" = \"configure\" ] || [ \"\$1\" = \"abort-upgrade\" ] || [ \"\$1\" = \"abort-deconfigure\" ] || [ \"\$1\" = \"abort-remove\" ] ; then
	# This will only remove masks created by d-s-h on package removal.
	deb-systemd-helper unmask 'skywire.service' >/dev/null || true

	# was-enabled defaults to true, so new installations run enable.
	if deb-systemd-helper --quiet was-enabled 'skywire.service'; then
		# Enables the unit on first installation, creates new
		# symlinks on upgrades if the unit file has changed.
		deb-systemd-helper enable 'skywire.service' >/dev/null || true
	else
		# Update the statefile to add new symlinks (if any), which need to be
		# cleaned up on purge. Also remove old symlinks.
		deb-systemd-helper update-state 'skywire.service' >/dev/null || true
	fi
fi
# End automatically added section
# Automatically added by dh_systemd_start/12.1.1
if [ \"\$1\" = \"configure\" ] || [ \"\$1\" = \"abort-upgrade\" ] || [ \"\$1\" = \"abort-deconfigure\" ] || [ \"\$1\" = \"abort-remove\" ] ; then
	if [ -d /run/systemd/system ]; then
		systemctl --system daemon-reload >/dev/null || true
		if [ -n \"\$2\" ]; then
			_dh_action=restart
		else
			_dh_action=start
		fi
		deb-systemd-invoke \$_dh_action 'skywire.service' >/dev/null || true
	fi
fi
# End automatically added section

#DEBHELPER#
EOF
	chmod 0555 "./debian/${REPONAME}.postinst"

	cat <<EOF >./debian/skywire.service
[Unit]
Description=Skywire Visor
After=network.target

[Service]
Type=simple
User=root
Group=root
ExecStart=/usr/bin/skywire-visor /opt/skywire/skywire-config.json
Restart=on-failure
RestartSec=20
TimeoutSec=30

[Install]
WantedBy=multi-user.target
EOF

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
