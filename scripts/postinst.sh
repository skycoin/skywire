#!/usr/bin/bash
#https://aur.archlinux.org/cgit/aur.git/tree/skywire.install?h=skywire-git
systemctl disable --now skywire-hypervisor.service
systemctl disable --now skywire-visor.service
#generate the config
skywire-cli visor gen-config -ro /etc/skywire-visor.json
#check if the hypervisorconfig package is installed
if [[ $(apt list --installed | grep hypervisorconfig) == *"hypervisorconfig"* ]]; then
  hvisorkey=$(cat /usr/lib/skycoin/skywire/hypervisor.txt)
  echo "Setting hypervisor key to $hvisorkey"
  echo "Starting visor"
  systemctl enable --now skywire-visor.service
else
  #configure hypervisor
  skywire-hypervisor gen-config -ro /etc/skywire-hypervisor.json
  hvisorkey=$(cat /etc/skywire-hypervisor.json | grep "public_key" | awk '{print substr($2,2,66)}')
  echo "Setting hypervisor key to $hvisorkey"
  #setting key and cert in hypervisor config file
  sed -i 's+"enable_tls": false,+"enable_tls": true,+g' /etc/skywire-hypervisor.json
	sed -i 's+"tls_cert_file": "",+"tls_cert_file": "/etc/skywire-hypervisor/cert.pem",+g' /etc/skywire-hypervisor.json
  sed -i 's+"tls_key_file": ""+"tls_key_file": "/etc/skywire-hypervisor/key.pem"+g' /etc/skywire-hypervisor.json
  echo "Starting hypervisor on 127.0.0.1:8000"
  systemctl enable --now skywire-hypervisor.service
  #add the hypervisor key to the visor's config
  sed -i 's/"hypervisors".*/"hypervisors": [{"public_key": "'"${hvisorkey}"'"}],/' /etc/skywire-visor.json
  echo "Starting visor"
  systemctl enable --now skywire-visor.service
  #create a package with the hypervisor key
#https://github.com/Skyfleet/packageconverter/blob/master/create-deb-pkg
packagename=hypervisorconfig
packageversion=0.0.1
packagearchitecture=$(dpkg --print-architecture)
debpkgdir="/usr/lib/skycoin/skywire/${packagename}-${packageversion}"
if [ -d "$debpkgdir" ]; then
  rm -rf "$debpkgdir"
fi
mkdir -p $debpkgdir/DEBIAN $debpkgdir/usr/lib/skycoin/skywire/
echo $hvisorkey > $debpkgdir/usr/lib/skycoin/skywire/hypervisor.txt
echo "Package: $packagename" > $debpkgdir/DEBIAN/control
echo "Version: $packageversion" >> $debpkgdir/DEBIAN/control
echo "Priority: optional" >> $debpkgdir/DEBIAN/control
echo "Section: web" >> $debpkgdir/DEBIAN/control
echo "Architecture: $packagearchitecture" >> $debpkgdir/DEBIAN/control
echo "Maintainer: SkycoinProject" >> $debpkgdir/DEBIAN/control
echo "Description: hypervisor public key" >> $debpkgdir/DEBIAN/control
cd /usr/lib/skycoin/skywire/
rm *.deb
dpkg-deb --build $debpkgdir
### create the apt repo at this point ###
cd /usr/lib/skycoin/skywire/
cp -b *.deb /var/cache/apt/archives/
#get debian version codename
checkremove="VERSION_CODENAME="
debian_codename=$(cat /etc/os-release | grep $checkremove)
debian_codename=${debian_codename#"$checkremove"}
repodirectory=/var/cache/apt/repo
mkdir -p $repodirectory
cd $repodirectory
[ ! -d "conf" ] && mkdir conf
if [ ! -f conf/distributions ]; then
echo "creating repo configuration file"
echo "Origin: localhost" > conf/distributions
echo "Label: localhost" >> conf/distributions
echo "Codename: $debian_codename" >> conf/distributions
echo "Architectures: $packagearchitecture" >> conf/distributions
echo "Components: main" >> conf/distributions
echo "Description: a local debian package repo" >> conf/distributions
fi
#create the repo
cd /var/cache/apt/archives
reprepro --basedir /var/cache/apt/repo includedeb $debian_codename *.deb
nohup readonlycache & exit
fi
