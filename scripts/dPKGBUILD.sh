#!/usr/bin/bash
# Maintainer: Moses Narrow <moe_narrow@use.startmail.com>
#create the debian package
#adapted from https://aur.archlinux.org/cgit/aur.git/tree/PKGBUILD?h=skywire-git
#usage: ./dPKGBUILD.sh architecture
set -e
pkgname=skywire
pkgdesc="Skywire Mainnet Node implementation. Skycoin.com"
pkgver=$(git describe --abbrev=0 | tr --delete v)
#increment pkgrel with any changes ; reset on updated version
pkgrel=1
#default to the system architecture if not provided as an argument to this script
if [ -z $1 ]; then
  pkgarch=$(dpkg --print-architecture)
else
  pkgarch=$1
fi
#support non-native builds for additional architectures here
[ $pkgarch == "amd64" ] && buildwith=(env GOOS=linux GOARCH=amd64)
[ $pkgarch == "arm64" ] && buildwith=(env GOOS=linux GOARCH=arm64)
[ $pkgarch == "armhf" ] && buildwith=(env GOOS=linux GOARCH=arm GOARM=6)

githuborg=SkycoinProject
pkggopath="github.com/${githuborg}/skywire-mainnet"

#srcdir and pkgdir are understood by makepkg on Arch
sourcedir=$(pwd)
sourcedir=${sourcedir}
srcdir=${sourcedir}/src
pkgdir=${sourcedir}/${pkgname}-${pkgver}-${pkgrel}-${pkgarch}

#add build deps here
makedepends=(go install npm python python2 sudo)
#add any runtime deps here
depends=(reprepro)

#check for make dependancies
for t in ${makedepends} ; do
    if [ -z "$(command -v "${t}")" ] ; then
        # not found
        error "Missing make dependancy '${t}'"
        error "Please install it and run this script again."
        return 1
    fi
done

info()
{
    printf '\033[0;32m[ INFO ]\033[0m %s\n' "${FUNCNAME[1]}: ${1}"
}

prepare() {
    #how to package golang applications on archlinux:
    # https://wiki.archlinux.org/index.php/Go_package_guidelines
  	mkdir -p ${srcdir}/go/src/github.com/${githuborg}/ ${srcdir}/go/bin ${srcdir}/go/apps
    ln -rTsf ${sourcedir} ${srcdir}/go/src/${pkggopath}
    ln -rTsf ${sourcedir} ${srcdir}/skywire-mainnet
  }


build() {
  export GOPATH=${srcdir}/go
  export GOBIN=${GOPATH}/bin
  export GOAPPS=${GOPATH}/apps
  export PATH=${GOPATH}/bin:${PATH}
  cd ${srcdir}/go/src/${pkggopath}
  info 'building binaries'
	cmddir=${srcdir}/go/src/${pkggopath}/cmd
  #using go build like this results in determinism - but `-trimpath` is unsupported in older versions!
	cd ${cmddir}/apps/skychat
  info 'building skychat binary'
  ${buildwith} go build -trimpath -ldflags '-extldflags ${LDFLAGS}' -ldflags=-buildid= -o $GOAPPS/ .
  cd ${cmddir}/apps/skysocks
  info 'building skysocks binary'
  ${buildwith} go build -trimpath -ldflags '-extldflags ${LDFLAGS}' -ldflags=-buildid= -o $GOAPPS/ .
  cd ${cmddir}/apps/skysocks-client
  info 'building skysocks-client binary'
  ${buildwith} go build -trimpath -ldflags '-extldflags ${LDFLAGS}' -ldflags=-buildid= -o $GOAPPS/ .
  cd ${cmddir}/skywire-visor
  info 'building skywire-visor binary'
  ${buildwith} go build -trimpath -ldflags '-extldflags ${LDFLAGS}' -ldflags=-buildid= -o $GOBIN/ .
  cd ${cmddir}/skywire-cli
  info 'building skywire-cli binary'
  ${buildwith} go build -trimpath -ldflags '-extldflags ${LDFLAGS}' -ldflags=-buildid= -o $GOBIN/ .
	cd ${cmddir}/setup-node
  info 'building setup-node binary'
  ${buildwith} go build -trimpath -ldflags '-extldflags ${LDFLAGS}' -ldflags=-buildid= -o $GOBIN/ .
	cd ${cmddir}/hypervisor
  info 'building hypervisor binary'
  ${buildwith} go build -trimpath -ldflags '-extldflags ${LDFLAGS}' -ldflags=-buildid= -o $GOBIN/ .
  #binary transparency
  cd $GOBIN
  sha256sum $(ls) > ${sourcedir}/${pkgname}-${pkgver}-${pkgrel}-${pkgarch}-checksums.txt
  cd $GOAPPS
  sha256sum $(ls) >> ${sourcedir}/${pkgname}-${pkgver}-${pkgrel}-${pkgarch}-checksums.txt
  #generate ssl key and cert
  cd ${srcdir}/go/src/${pkggopath}/static/skywire-manager-src/ssl/
  chmod +x *.sh
  sudo ./generate.sh
  #build the readonly-cache binary
  cd ${srcdir}/go/src/${pkggopath}/scripts/
  go build -trimpath -ldflags '-extldflags ${LDFLAGS}' -ldflags=-buildid= -o $GOBIN/ readonlycache.go
}

package() {
  #create directory trees
  sudo mkdir -p ${pkgdir}/usr/bin/apps
  sudo mkdir -p ${pkgdir}/etc/skywire
  sudo mkdir -p ${pkgdir}/DEBIAN
  #create control file
  echo "Package: ${pkgname}" > ${srcdir}/control
  echo "Version: ${pkgver}" >> ${srcdir}/control
  echo "Priority: optional" >> ${srcdir}/control
  echo "Section: web" >> ${srcdir}/control
  echo "Architecture: ${pkgarch}" >> ${srcdir}/control
  echo "Depends: ${depends}" >> ${srcdir}/control
  echo "Maintainer: SkycoinProject" >> ${srcdir}/control
  echo "Description: ${pkgdesc}" >> ${srcdir}/control
  info 'installing binaries'
  sudo install -Dm755 ${srcdir}/control ${pkgdir}/DEBIAN/control
  sudo install -Dm755 ${srcdir}/go/src/${pkggopath}/scripts/postinst.sh ${pkgdir}/DEBIAN/postinst
  #install binaries
  sudo install -Dm755 ${srcdir}/go/bin/hypervisor ${pkgdir}/usr/bin/skywire-hypervisor
  sudo install -Dm755 ${srcdir}/go/bin/skywire-visor ${pkgdir}/usr/bin/skywire-visor
  sudo install -Dm755 ${srcdir}/go/bin/skywire-cli ${pkgdir}/usr/bin/skywire-cli
  sudo install -Dm755 ${srcdir}/go/apps/skychat ${pkgdir}/usr/bin/apps/skychat
  sudo install -Dm755 ${srcdir}/go/apps/skysocks ${pkgdir}/usr/bin/apps/skysocks
  sudo install -Dm755 ${srcdir}/go/apps/skysocks-client ${pkgdir}/usr/bin/apps/skysocks-client
  sudo install -Dm755 ${srcdir}/go/bin/readonlycache ${pkgdir}/usr/bin/readonlycache
  #install the system.d services
  sudo install -Dm644 ${srcdir}/skywire-mainnet/init/skywire-hypervisor.service ${pkgdir}/etc/systemd/system/skywire-hypervisor.service
  sudo install -Dm644 ${srcdir}/skywire-mainnet/init/skywire-visor.service ${pkgdir}/etc/systemd/system/skywire-visor.service
  #install the tls key & cert
  sudo install -Dm644 ${srcdir}/go/src/${pkggopath}/static/skywire-manager-src/ssl/server.crt ${pkgdir}/etc/skywire-hypervisor/cert.pem
  sudo install -Dm644 ${srcdir}/go/src/${pkggopath}/static/skywire-manager-src/ssl/server.key ${pkgdir}/etc/skywire-hypervisor/key.pem
  #create the debian package
  dpkg-deb --build ${pkgdir}
}

main_build()
{
    prepare || error "Failure occured in prepare()" return 1

    build || error "Failure occured in build()" return 1

    package || error "Failure occured in package()" return 1

    sudo rm -rf $pkgdir $srcdir
}


case "$1" in
*)
    main_build || (error "Failed." && exit 1)
    ;;
esac
