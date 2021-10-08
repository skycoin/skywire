#!/bin/bash

VER=$1

AUTHOREMAIL=$2
AUTHORNAME=$3

USAGE="Usage: bash package.sh RELEASE_VERSION AUTHOR_EMAIL AUTHOR_FULL_NAME"

if [ -z "$1" ]
then
	echo "$USAGE"
	exit
fi

if [ -z "$2" ]
then
	echo "$USAGE"
	exit
fi

if [ -z "$3" ]
then
	echo "$USAGE"
	exit
fi

ORGNAME=skycoin
REPONAME=skywire
BRANCH=$(git rev-parse --abbrev-ref HEAD)

function create_control_file {
	if [ -z "$1" ]
	then
		exit
	fi

	ARCH=$1

	echo "Source: $REPONAME" >> ./debian/control
	echo "Maintainer: $AUTHORNAME <$AUTHOREMAIL>" >> ./debian/control
	echo "Standards-Version: $VER" >> ./debian/control
	echo "Section: base" >> ./debian/control
	echo "Build-Depends: dh-systemd (>= 1.5)" >> ./debian/control
	echo "" >> ./debian/control
	echo "Package: $REPONAME" >> ./debian/control
	echo "Priority: optional" >> ./debian/control
	echo "Architecture: $ARCH" >> ./debian/control
	echo "Description: Skywire applications to participate in" >> "./debian/control"
	echo " skywire network" >> "./debian/control"
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

	GOOS=linux GOARCH="$GOARCH" make bin
	GOOS=linux GOARCH="$GOARCH" make host-apps

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
	echo "distclean:" >> ./Makefile
	echo "	echo dummy" >> ./Makefile
	echo "install:" >> ./Makefile
	echo "	mkdir -p \$(DESTDIR)/opt/skywire/apps" >> ./Makefile
	echo "	mkdir -p \$(DESTDIR)/usr/bin" >> ./Makefile
	echo "	install -m 0755 skywire-visor \$(DESTDIR)/opt/skywire/skywire-visor" >> ./Makefile
	echo "	install -m 0755 skywire-cli \$(DESTDIR)/opt/skywire/skywire-cli" >> ./Makefile
	echo "	install -m 0755 apps/skychat \$(DESTDIR)/opt/skywire/apps/skychat" >> ./Makefile
	echo "	install -m 0755 apps/skysocks \$(DESTDIR)/opt/skywire/apps/skysocks" >> ./Makefile
	echo "	install -m 0755 apps/skysocks-client \$(DESTDIR)/opt/skywire/apps/skysocks-client" >> ./Makefile
	echo "	install -m 0755 apps/vpn-server \$(DESTDIR)/opt/skywire/apps/vpn-server" >> ./Makefile
	echo "	install -m 0755 apps/vpn-client \$(DESTDIR)/opt/skywire/apps/vpn-client" >> ./Makefile
	echo "	ln -s /opt/skywire/skywire-visor \$(DESTDIR)/usr/bin/skywire-visor" >> ./Makefile
	echo "	ln -s /opt/skywire/skywire-cli \$(DESTDIR)/usr/bin/skywire-cli" >> ./Makefile
	echo "" >> ./Makefile
	echo "uninstall:" >> ./Makefile
	echo "	rm -rf \$(DESTDIR)/usr/bin/skywire-visor" >> ./Makefile
	echo "	rm -rf \$(DESTDIR)/usr/bin/skywire-cli" >> ./Makefile
	echo "	rm -rf \$(DESTDIR)/opt/skywire" >> ./Makefile

	mkdir ./debian

	create_control_file "$ARCH"

	# this will bring up the editor
	DEBEMAIL=$AUTHOREMAIL DEBFULLNAME=$AUTHORNAME dch --create -d --distribution stable

	echo "#!/usr/bin/make -f" >> ./debian/rules
	echo "%:" >> ./debian/rules
	echo "	dh \$@ --with systemd" >> ./debian/rules

	echo "Copyright $(date +%Y), Skycoin." >> ./debian/copyright

	echo "10" >> ./debian/compat

	echo "#!/bin/bash" >> "./debian/${REPONAME}.prerm"
	echo "" >> "./debian/${REPONAME}.prerm"
	echo "touch /opt/skywire/removing" >> "./debian/${REPONAME}.prerm"
	echo "" >> "./debian/${REPONAME}.prerm"
	echo "# Automatically added by dh_systemd_start/12.1.1" >> "./debian/${REPONAME}.prerm"
	echo "if [ -d /run/systemd/system ] && [ \"\$1\" = remove ]; then" >> "./debian/${REPONAME}.prerm"
  echo "	deb-systemd-invoke stop 'skywire.service' >/dev/null || true" >> "./debian/${REPONAME}.prerm"
  echo "fi" >> "./debian/${REPONAME}.prerm"
  echo "# End automatically added section" >> "./debian/${REPONAME}.prerm"
	echo "" >> "./debian/${REPONAME}.prerm"
	echo "#DEBHELPER#" >> "./debian/${REPONAME}.prerm"

	chmod 0555 "./debian/${REPONAME}.prerm"

	echo "#!/bin/bash" >> "./debian/${REPONAME}.preinst"
	echo "" >> "./debian/${REPONAME}.preinst"
	echo "if [ -f "/opt/skywire/removing" ]" >> "./debian/${REPONAME}.preinst"
	echo "then" >> "./debian/${REPONAME}.preinst"
	echo "	touch /opt/skywire/upgrading" >> "./debian/${REPONAME}.preinst"
	echo "fi" >> "./debian/${REPONAME}.preinst"
	echo "" >> "./debian/${REPONAME}.preinst"
	echo "#DEBHELPER#" >> "./debian/${REPONAME}.preinst"

	chmod 0555 "./debian/${REPONAME}.preinst"

	echo "#!/bin/bash" >> "./debian/${REPONAME}.postrm"
	echo "" >> "./debian/${REPONAME}.postrm"
	echo "if [ ! -f "opt/skywire/upgrading" ]" >> "./debian/${REPONAME}.postrm"
	echo "then" >> "./debian/${REPONAME}.postrm"
	echo "	rm -rf /opt/skywire" >> "./debian/${REPONAME}.postrm"
	echo "fi" >> "./debian/${REPONAME}.postrm"
	echo "" >> "./debian/${REPONAME}.postrm"
	echo "# Automatically added by dh_systemd_start/12.1.1" >> "./debian/${REPONAME}.postrm"
	echo "if [ -d /run/systemd/system ]; then" >> "./debian/${REPONAME}.postrm"
	echo "	systemctl --system daemon-reload >/dev/null || true" >> "./debian/${REPONAME}.postrm"
	echo "fi" >> "./debian/${REPONAME}.postrm"
	echo "# End automatically added section" >> "./debian/${REPONAME}.postrm"
	echo "# Automatically added by dh_systemd_enable/12.1.1" >> "./debian/${REPONAME}.postrm"
	echo "if [ \"\$1\" = \"remove\" ]; then" >> "./debian/${REPONAME}.postrm"
	echo "	if [ -x \"/usr/bin/deb-systemd-helper\" ]; then" >> "./debian/${REPONAME}.postrm"
	echo "		deb-systemd-helper mask 'skywire.service' >/dev/null || true" >> "./debian/${REPONAME}.postrm"
	echo "	fi" >> "./debian/${REPONAME}.postrm"
	echo "fi" >> "./debian/${REPONAME}.postrm"
	echo "" >> "./debian/${REPONAME}.postrm"
	echo "if [ \"\$1\" = \"purge\" ]; then" >> "./debian/${REPONAME}.postrm"
	echo "	if [ -x \"/usr/bin/deb-systemd-helper\" ]; then" >> "./debian/${REPONAME}.postrm"
	echo "		deb-systemd-helper purge 'skywire.service' >/dev/null || true" >> "./debian/${REPONAME}.postrm"
	echo "		deb-systemd-helper unmask 'skywire.service' >/dev/null || true" >> "./debian/${REPONAME}.postrm"
	echo "	fi" >> "./debian/${REPONAME}.postrm"
	echo "fi" >> "./debian/${REPONAME}.postrm"
	echo "# End automatically added section" >> "./debian/${REPONAME}.postrm"
	echo "" >> "./debian/${REPONAME}.postrm"
	echo "#DEBHELPER#" >> "./debian/${REPONAME}.postrm"

	chmod 0555 "./debian/${REPONAME}.postrm"

	echo "#!/bin/bash" >> "./debian/${REPONAME}.postinst"
	echo "" >> "./debian/${REPONAME}.postinst"
	echo "rm -rf /opt/skywire/upgrading" >> "./debian/${REPONAME}.postinst"
	echo "if [ -f "/opt/skywire/removing" ]" >> "./debian/${REPONAME}.postinst"
	echo "then" >> "./debian/${REPONAME}.postinst"
	echo "	rm -rf /opt/skywire/removing" >> "./debian/${REPONAME}.postinst"
	echo "else" >> "./debian/${REPONAME}.postinst"
	echo "	/opt/skywire/skywire-cli visor gen-config -o /opt/skywire/skywire-config.json" >> "./debian/${REPONAME}.postinst"
	echo "fi" >> "./debian/${REPONAME}.postinst"
	echo "" >> "./debian/${REPONAME}.postinst"
	echo "setcap 'cap_net_admin+p' /opt/skywire/apps/vpn-client" >> "./debian/${REPONAME}.postinst"
	echo "" >> "./debian/${REPONAME}.postinst"
	echo "# Automatically added by dh_systemd_enable/12.1.1" >> "./debian/${REPONAME}.postinst"
	echo "if [ \"\$1\" = \"configure\" ] || [ \"\$1\" = \"abort-upgrade\" ] || [ \"\$1\" = \"abort-deconfigure\" ] || [ \"\$1\" = \"abort-remove\" ] ; then" >> "./debian/${REPONAME}.postinst"
	echo "	# This will only remove masks created by d-s-h on package removal." >> "./debian/${REPONAME}.postinst"
	echo "	deb-systemd-helper unmask 'skywire.service' >/dev/null || true" >> "./debian/${REPONAME}.postinst"
	echo "" >> "./debian/${REPONAME}.postinst"
	echo "	# was-enabled defaults to true, so new installations run enable." >> "./debian/${REPONAME}.postinst"
	echo "	if deb-systemd-helper --quiet was-enabled 'skywire.service'; then" >> "./debian/${REPONAME}.postinst"
	echo "		# Enables the unit on first installation, creates new" >> "./debian/${REPONAME}.postinst"
	echo "		# symlinks on upgrades if the unit file has changed." >> "./debian/${REPONAME}.postinst"
	echo "		deb-systemd-helper enable 'skywire.service' >/dev/null || true" >> "./debian/${REPONAME}.postinst"
	echo "	else" >> "./debian/${REPONAME}.postinst"
	echo "		# Update the statefile to add new symlinks (if any), which need to be" >> "./debian/${REPONAME}.postinst"
	echo "		# cleaned up on purge. Also remove old symlinks." >> "./debian/${REPONAME}.postinst"
	echo "		deb-systemd-helper update-state 'skywire.service' >/dev/null || true" >> "./debian/${REPONAME}.postinst"
	echo "	fi" >> "./debian/${REPONAME}.postinst"
	echo "fi" >> "./debian/${REPONAME}.postinst"
	echo "# End automatically added section" >> "./debian/${REPONAME}.postinst"
	echo "# Automatically added by dh_systemd_start/12.1.1" >> "./debian/${REPONAME}.postinst"
	echo "if [ \"\$1\" = \"configure\" ] || [ \"\$1\" = \"abort-upgrade\" ] || [ \"\$1\" = \"abort-deconfigure\" ] || [ \"\$1\" = \"abort-remove\" ] ; then" >> "./debian/${REPONAME}.postinst"
	echo "	if [ -d /run/systemd/system ]; then" >> "./debian/${REPONAME}.postinst"
	echo "		systemctl --system daemon-reload >/dev/null || true" >> "./debian/${REPONAME}.postinst"
	echo "		if [ -n \"\$2\" ]; then" >> "./debian/${REPONAME}.postinst"
	echo "			_dh_action=restart" >> "./debian/${REPONAME}.postinst"
	echo "		else" >> "./debian/${REPONAME}.postinst"
	echo "			_dh_action=start" >> "./debian/${REPONAME}.postinst"
	echo "		fi" >> "./debian/${REPONAME}.postinst"
	echo "		deb-systemd-invoke \$_dh_action 'skywire.service' >/dev/null || true" >> "./debian/${REPONAME}.postinst"
	echo "	fi" >> "./debian/${REPONAME}.postinst"
	echo "fi" >> "./debian/${REPONAME}.postinst"
	echo "# End automatically added section" >> "./debian/${REPONAME}.postinst"
	echo "" >> "./debian/${REPONAME}.postinst"
	echo "#DEBHELPER#" >> "./debian/${REPONAME}.postinst"

	chmod 0555 "./debian/${REPONAME}.postinst"

	echo "[Unit]" >> ./debian/skywire.service
	echo "Description=Skywire Visor" >> ./debian/skywire.service
	echo "After=network.target" >> ./debian/skywire.service
  echo "" >> ./debian/skywire.service
  echo "[Service]" >> ./debian/skywire.service
  echo "Type=simple" >> ./debian/skywire.service
  echo "User=root" >> ./debian/skywire.service
  echo "Group=root" >> ./debian/skywire.service
  echo "ExecStart=/usr/bin/skywire-visor /opt/skywire/skywire-config.json" >> ./debian/skywire.service
  echo "Restart=on-failure" >> ./debian/skywire.service
  echo "RestartSec=20" >> ./debian/skywire.service
  echo "TimeoutSec=30" >> ./debian/skywire.service
  echo "" >> ./debian/skywire.service
  echo "[Install]" >> ./debian/skywire.service
  echo "WantedBy=multi-user.target" >> ./debian/skywire.service

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
