#!/bin/bash

# TODO: consider using bash here document (cat<<EOF) instead of echo "..." for appending things.

ver=$1

authoremail=$2
authorname=$3

usage="Usage: bash package_deb.sh RELEASE_VERSION AUTHOR_EMAIL AUTHOR_FULL_NAME"

if [ -z "$1" ]; then
  echo "$usage"
  exit
fi

if [ -z "$2" ]; then
  echo "$usage"
  exit
fi

if [ -z "$3" ]; then
  echo "$usage"
  exit
fi

orgname=skycoin
reponame=skywire
branch=master

function create_control_file() {
  if [ -z "$1" ]; then
    exit
  fi

  arch=$1

  # shellcheck disable=SC2129
  echo "Source: $reponame" >>./debian/control
  echo "Maintainer: $authorname <$authoremail>" >>./debian/control
  echo "Standards-Version: $ver" >>./debian/control
  echo "Section: base" >>./debian/control
  echo "Build-Depends: dh-systemd (>= 1.5)" >>./debian/control
  echo "" >>./debian/control
  echo "Package: $reponame" >>./debian/control
  echo "Priority: optional" >>./debian/control
  echo "Architecture: $arch" >>./debian/control
  echo "Description: Skywire applications to participate in" >>"./debian/control"
  echo " skywire network" >>"./debian/control"
}

function pack_deb() {
  if [ -z "$1" ]; then
    exit
  fi

  if [ -z "$2" ]; then
    exit
  fi

  arch=$1
  goarch=$2

  GOOS=linux GOARCH="$goarch" make bin
  GOOS=linux GOARCH="$goarch" make host-apps

  mkdir packages
  cd ./packages

  mkdir "./$reponame-$ver"
  cp ../skywire-visor "./$reponame-$ver/"
  cp ../skywire-cli "./$reponame-$ver/"
  mkdir "./$reponame-$ver/apps"
  cp ../apps/skychat "./$reponame-$ver/apps/"
  cp ../apps/skysocks "./$reponame-$ver/apps/"
  cp ../apps/skysocks-client "./$reponame-$ver/apps/"
  cp ../apps/vpn-client "./$reponame-$ver/apps/"
  cp ../apps/vpn-server "./$reponame-$ver/apps/"

  cd "./$reponame-$ver"

  # well, okay, this may seem unnecessary, but for some reason the lack of distclean target
  # prevents debuild from passing DESTDIR correctly. This is sick, but works this way
  read -r -d '' mkrecipes <<"EOF"
distclean:
\techo "dummy"

install:
\tmkdir -p \$(DESTDIR)/opt/skywire/apps
\tmkdir -p \$(DESTDIR)/usr/bin
\tinstall -m 0755 skywire-visor \$(DESTDIR)/opt/skywire/skywire-visor
\tinstall -m 0755 skywire-cli \$(DESTDIR)/opt/skywire/skywire-cli
\tinstall -m 0755 apps/skychat \$(DESTDIR)/opt/skywire/apps/skychat
\tinstall -m 0755 apps/skysocks \$(DESTDIR)/opt/skywire/apps/skysocks
\tinstall -m 0755 apps/skysocks-client \$(DESTDIR)/opt/skywire/apps/skysocks-client
\tinstall -m 0755 apps/vpn-server \$(DESTDIR)/opt/skywire/apps/vpn-server
\tinstall -m 0755 apps/vpn-client \$(DESTDIR)/opt/skywire/apps/vpn-client
\tln -s /opt/skywire/skywire-visor \$(DESTDIR)/usr/bin/skywire-visor
\tln -s /opt/skywire/skywire-cli \$(DESTDIR)/usr/bin/skywire-cli

uninstall:
\trm -rf \$(DESTDIR)/usr/bin/skywire-visor
\trm -rf \$(DESTDIR)/usr/bin/skywire-cli
\trm -rf \$(DESTDIR)/opt/skywire
EOF

  echo -e "$mkrecipes" >>./Makefile

  mkdir ./debian

  create_control_file "$arch"

  # this will bring up the editor
  DEBEMAIL=$authoremail DEBFULLNAME=$authorname dch --create -d --distribution stable

  # shellcheck disable=SC2129
  echo "#!/usr/bin/make -f" >>./debian/rules
  echo "%:" >>./debian/rules
  echo "	dh \$@ --with systemd" >>./debian/rules

  echo "Copyright $(date +%Y), Skycoin." >>./debian/copyright

  echo "10" >>./debian/compat

  # shellcheck disable=SC2129
  echo "#!/bin/bash" >>"./debian/${reponame}.prerm"
  echo "" >>"./debian/${reponame}.prerm"
  echo "touch /opt/skywire/removing" >>"./debian/${reponame}.prerm"
  echo "" >>"./debian/${reponame}.prerm"
  echo "# Automatically added by dh_systemd_start/12.1.1" >>"./debian/${reponame}.prerm"
  echo "if [ -d /run/systemd/system ] && [ \"\$1\" = remove ]; then" >>"./debian/${reponame}.prerm"
  echo "	deb-systemd-invoke stop 'skywire.service' >/dev/null || true" >>"./debian/${reponame}.prerm"
  echo "fi" >>"./debian/${reponame}.prerm"
  echo "# End automatically added section" >>"./debian/${reponame}.prerm"
  echo "" >>"./debian/${reponame}.prerm"
  echo "#DEBHELPER#" >>"./debian/${reponame}.prerm"

  chmod 0555 "./debian/${reponame}.prerm"

  # shellcheck disable=SC2129
  echo "#!/bin/bash" >>"./debian/${reponame}.preinst"
  echo "" >>"./debian/${reponame}.preinst"
  echo "if [ -f /opt/skywire/removing ]" >>"./debian/${reponame}.preinst"
  echo "then" >>"./debian/${reponame}.preinst"
  echo "	touch /opt/skywire/upgrading" >>"./debian/${reponame}.preinst"
  echo "fi" >>"./debian/${reponame}.preinst"
  echo "" >>"./debian/${reponame}.preinst"
  echo "#DEBHELPER#" >>"./debian/${reponame}.preinst"

  chmod 0555 "./debian/${reponame}.preinst"

  # shellcheck disable=SC2129
  echo "#!/bin/bash" >>"./debian/${reponame}.postrm"
  echo "" >>"./debian/${reponame}.postrm"
  echo "if [ ! -f /opt/skywire/upgrading ]" >>"./debian/${reponame}.postrm"
  echo "then" >>"./debian/${reponame}.postrm"
  echo "	rm -rf /opt/skywire" >>"./debian/${reponame}.postrm"
  echo "fi" >>"./debian/${reponame}.postrm"
  echo "" >>"./debian/${reponame}.postrm"
  echo "# Automatically added by dh_systemd_start/12.1.1" >>"./debian/${reponame}.postrm"
  echo "if [ -d /run/systemd/system ]; then" >>"./debian/${reponame}.postrm"
  echo "	systemctl --system daemon-reload >/dev/null || true" >>"./debian/${reponame}.postrm"
  echo "fi" >>"./debian/${reponame}.postrm"
  echo "# End automatically added section" >>"./debian/${reponame}.postrm"
  echo "# Automatically added by dh_systemd_enable/12.1.1" >>"./debian/${reponame}.postrm"
  echo "if [ \"\$1\" = \"remove\" ]; then" >>"./debian/${reponame}.postrm"
  echo "	if [ -x \"/usr/bin/deb-systemd-helper\" ]; then" >>"./debian/${reponame}.postrm"
  echo "		deb-systemd-helper mask 'skywire.service' >/dev/null || true" >>"./debian/${reponame}.postrm"
  echo "	fi" >>"./debian/${reponame}.postrm"
  echo "fi" >>"./debian/${reponame}.postrm"
  echo "" >>"./debian/${reponame}.postrm"
  echo "if [ \"\$1\" = \"purge\" ]; then" >>"./debian/${reponame}.postrm"
  echo "	if [ -x \"/usr/bin/deb-systemd-helper\" ]; then" >>"./debian/${reponame}.postrm"
  echo "		deb-systemd-helper purge 'skywire.service' >/dev/null || true" >>"./debian/${reponame}.postrm"
  echo "		deb-systemd-helper unmask 'skywire.service' >/dev/null || true" >>"./debian/${reponame}.postrm"
  echo "	fi" >>"./debian/${reponame}.postrm"
  echo "fi" >>"./debian/${reponame}.postrm"
  echo "# End automatically added section" >>"./debian/${reponame}.postrm"
  echo "" >>"./debian/${reponame}.postrm"
  echo "#DEBHELPER#" >>"./debian/${reponame}.postrm"

  chmod 0555 "./debian/${reponame}.postrm"

  # shellcheck disable=SC2129
  echo "#!/bin/bash" >>"./debian/${reponame}.postinst"
  echo "" >>"./debian/${reponame}.postinst"
  echo "rm -rf /opt/skywire/upgrading" >>"./debian/${reponame}.postinst"
  echo "if [ -f /opt/skywire/removing ]" >>"./debian/${reponame}.postinst"
  echo "then" >>"./debian/${reponame}.postinst"
  echo "	rm -rf /opt/skywire/removing" >>"./debian/${reponame}.postinst"
  echo "else" >>"./debian/${reponame}.postinst"
  echo "	/opt/skywire/skywire-cli visor gen-config -o /opt/skywire/skywire-config.json" >>"./debian/${reponame}.postinst"
  echo "fi" >>"./debian/${reponame}.postinst"
  echo "" >>"./debian/${reponame}.postinst"
  echo "setcap 'cap_net_admin+p' /opt/skywire/apps/vpn-client" >>"./debian/${reponame}.postinst"
  echo "" >>"./debian/${reponame}.postinst"
  echo "# Automatically added by dh_systemd_enable/12.1.1" >>"./debian/${reponame}.postinst"
  echo "if [ \"\$1\" = \"configure\" ] || [ \"\$1\" = \"abort-upgrade\" ] || [ \"\$1\" = \"abort-deconfigure\" ] || [ \"\$1\" = \"abort-remove\" ] ; then" >>"./debian/${reponame}.postinst"
  echo "	# This will only remove masks created by d-s-h on package removal." >>"./debian/${reponame}.postinst"
  echo "	deb-systemd-helper unmask 'skywire.service' >/dev/null || true" >>"./debian/${reponame}.postinst"
  echo "" >>"./debian/${reponame}.postinst"
  echo "	# was-enabled defaults to true, so new installations run enable." >>"./debian/${reponame}.postinst"
  echo "	if deb-systemd-helper --quiet was-enabled 'skywire.service'; then" >>"./debian/${reponame}.postinst"
  echo "		# Enables the unit on first installation, creates new" >>"./debian/${reponame}.postinst"
  echo "		# symlinks on upgrades if the unit file has changed." >>"./debian/${reponame}.postinst"
  echo "		deb-systemd-helper enable 'skywire.service' >/dev/null || true" >>"./debian/${reponame}.postinst"
  echo "	else" >>"./debian/${reponame}.postinst"
  echo "		# Update the statefile to add new symlinks (if any), which need to be" >>"./debian/${reponame}.postinst"
  echo "		# cleaned up on purge. Also remove old symlinks." >>"./debian/${reponame}.postinst"
  echo "		deb-systemd-helper update-state 'skywire.service' >/dev/null || true" >>"./debian/${reponame}.postinst"
  echo "	fi" >>"./debian/${reponame}.postinst"
  echo "fi" >>"./debian/${reponame}.postinst"
  echo "# End automatically added section" >>"./debian/${reponame}.postinst"
  echo "# Automatically added by dh_systemd_start/12.1.1" >>"./debian/${reponame}.postinst"
  echo "if [ \"\$1\" = \"configure\" ] || [ \"\$1\" = \"abort-upgrade\" ] || [ \"\$1\" = \"abort-deconfigure\" ] || [ \"\$1\" = \"abort-remove\" ] ; then" >>"./debian/${reponame}.postinst"
  echo "	if [ -d /run/systemd/system ]; then" >>"./debian/${reponame}.postinst"
  echo "		systemctl --system daemon-reload >/dev/null || true" >>"./debian/${reponame}.postinst"
  echo "		if [ -n \"\$2\" ]; then" >>"./debian/${reponame}.postinst"
  echo "			_dh_action=restart" >>"./debian/${reponame}.postinst"
  echo "		else" >>"./debian/${reponame}.postinst"
  echo "			_dh_action=start" >>"./debian/${reponame}.postinst"
  echo "		fi" >>"./debian/${reponame}.postinst"
  echo "		deb-systemd-invoke \$_dh_action 'skywire.service' >/dev/null || true" >>"./debian/${reponame}.postinst"
  echo "	fi" >>"./debian/${reponame}.postinst"
  echo "fi" >>"./debian/${reponame}.postinst"
  echo "# End automatically added section" >>"./debian/${reponame}.postinst"
  echo "" >>"./debian/${reponame}.postinst"
  echo "#DEBHELPER#" >>"./debian/${reponame}.postinst"

  chmod 0555 "./debian/${reponame}.postinst"

  # shellcheck disable=SC2129
  echo "[Unit]" >>./debian/skywire.service
  echo "Description=Skywire Visor" >>./debian/skywire.service
  echo "After=network.target" >>./debian/skywire.service
  echo "" >>./debian/skywire.service
  echo "[Service]" >>./debian/skywire.service
  echo "Type=simple" >>./debian/skywire.service
  echo "User=root" >>./debian/skywire.service
  echo "Group=root" >>./debian/skywire.service
  echo "ExecStart=/usr/bin/skywire-visor /opt/skywire/skywire-config.json" >>./debian/skywire.service
  echo "Restart=on-failure" >>./debian/skywire.service
  echo "RestartSec=20" >>./debian/skywire.service
  echo "TimeoutSec=30" >>./debian/skywire.service
  echo "" >>./debian/skywire.service
  echo "[Install]" >>./debian/skywire.service
  echo "WantedBy=multi-user.target" >>./debian/skywire.service

  DEBEMAIL=$authoremail DEBFULLNAME=$authorname debuild -a"$arch" -us -uc

  cd ..
  echo "$PWD"
  ls -la
  mv "./${reponame}_${ver}-1_${arch}.deb" ../../../deb/
  cd ..
  rm -rf ./packages
  rm -rf ./debian
}

set -euo pipefail

sudo dpkg --add-architecture armhf

rm -rf ./deb
mkdir ./deb

mkdir ./packaging
cd ./packaging
git clone https://github.com/${orgname}/${reponame} --branch "$branch" --depth 1
cd "./$reponame" || exit

pack_deb amd64 amd64
pack_deb i386 386
pack_deb arm64 arm64
pack_deb arm arm
pack_deb armhf arm

cd ../..
rm -rf "packaging"
