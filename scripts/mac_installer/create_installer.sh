#!/usr/bin/env bash

# variables
mac_script_dir="./scripts/mac_installer"
installer_build_dir="./mac_build"
installer_package_dir="${installer_build_dir}"/binaries/Skywire.app
git_tag=$(git describe --tags)
date_format=$(date -u "+%Y-%m-%d")
go_arch=$(go env GOARCH) # build for amd64 and arm64 from single host
sign_binary=false
notarize_binary=false
staple_notarization=false
package_path=
developer_id=
output=

greent='\033[0;32m'
yellowt='\033[0;33m'
nct='\033[0m' # No Color

# Has to be run from MacOS Host
current_os="$(uname -s)"
if [[ "$current_os" != "Darwin" ]]; then
  echo "Can only be run from MacOS Host"
  exit 1
fi

function print_usage() {
  echo "Usage: sh create_installer.sh [-o|--output output_skywire_dir] [-s | --sign signs the binary] [-n | --notarize notarize the binary ] [-t <PACKAGE_PATH> | --staple <PACKAGE_PATH> ]"
  echo "You need to provide the following environment variables if you want to sign and notarize the binary:"
  echo -e "${greent}MAC_HASH_APPLICATION_ID${nct}: Hash of Developer ID Application"
  echo -e "${greent}MAC_HASH_INSTALLER_ID${nct}  : Hash of Developer ID Installer"
  echo -e "${greent}MAC_DEVELOPER_USERNAME${nct} : Developer Account Email"
  echo -e "${greent}MAC_DEVELOPER_PASSWORD${nct} : Application specific / Apple ID password ${yellowt}https://support.apple.com/en-us/HT204397${nct}"
}

function staple_installer() {
  if [ -z "$package_path" ]; then
    echo "package_path of option -t cannot be empty"
  fi
  xcrun stapler staple "${package_path}"
}

function build_installer() {
  set -euo pipefail

  if [ -z "$output" ]; then
    output="${PWD}/"
    echo "No output flag provided, storing installer to the current directory: ${output}"
  else
    # shellcheck disable=SC2039
    if [ "${output:(-1)}" != "/" ]; then
      output="${output}/"
    fi

    echo "Storing installer to ${output}"
  fi

  # build skywire binariea
  make CGO_ENABLED=1 GOOS=darwin GOARCH="${go_arch}" build-systray

  if [ -d ${installer_build_dir}/binaries/Skywire.app ]; then
    rm -rf ${installer_build_dir}/binaries/Skywire.app
  fi

  # Create directories
  mkdir -p ${installer_build_dir}/binaries/Skywire.app
  mkdir -p ${installer_package_dir}/Contents/{Resources,MacOS/apps}

  # build deinstaller
  go build -o ${installer_package_dir}/Contents/MacOS/deinstaller ${mac_script_dir}/desktop-deinstaller

  # prepare Distribution.xml
  cp ${mac_script_dir}/Distribution.xml ${installer_build_dir}/

  # modify version info
  cp ${mac_script_dir}/AppInfo.plist.tmpl ${installer_package_dir}/Contents/Info.plist
  perl -i -pe "s/{{BundleVersion}}/${git_tag}/g" ${installer_package_dir}/Contents/Info.plist

  cp ${mac_script_dir}/icon.icns ${installer_package_dir}/Contents/Resources/icon.icns
  cp ${mac_script_dir}/icon.tiff ${installer_package_dir}/Contents/Resources/icon.tiff
  mv ./skywire-visor ${installer_package_dir}/Contents/MacOS/skywire-visor
  mv ./skywire-cli ${installer_package_dir}/Contents/MacOS/skywire-cli
  mv ./apps/vpn-client ${installer_package_dir}/Contents/MacOS/apps/vpn-client

  cat <<EOF >${installer_package_dir}/Contents/MacOS/Skywire
  #!/usr/bin/env bash

  osascript -e "do shell script \"/Applications/Skywire.app/Contents/MacOS/skywire-visor --systray >> /Users/\${USER}/Library/Logs/skywire/visor.log\" with administrator privileges"

EOF

  chmod +x ${installer_package_dir}/Contents/MacOS/Skywire

  # https://stackoverflow.com/a/21210966
  if [ "$sign_binary" == true ]; then
    echo "signing the binary using codesign"

    if [ -z "$MAC_HASH_APPLICATION_ID" ]; then
      echo -e "${yellowt}environment MAC_HASH_APPLICATION_ID has to be set before you sign the binary${nct}"
      exit 1
    fi

    #    mv ${installer_package_dir} /tmp/Skywire.app
    codesign --verbose --deep --force --options=runtime --sign "$MAC_HASH_APPLICATION_ID" --timestamp "$installer_package_dir"

    #    mv /tmp/Skywire.app "${installer_build_dir}"/binaries/Skywireapp
  fi

  # prepare install scripts
  mkdir -p ${installer_build_dir}/{install,update,remove}_scripts
  cp -Rv ${mac_script_dir}/install_scripts/* ${installer_build_dir}/install_scripts/
  cp -Rv ${mac_script_dir}/update_scripts/* ${installer_build_dir}/update_scripts/
  cp -Rv ${mac_script_dir}/remove_scripts/* ${installer_build_dir}/remove_scripts/

  # build installer
  pkgbuild --root ${installer_build_dir}/binaries --identifier com.skycoin.skywire.visor --install-location /Applications/ --scripts ${installer_build_dir}/install_scripts ${installer_build_dir}/installer.pkg
  pkgbuild --root ${installer_build_dir}/binaries --identifier com.skycoin.skywire.updater --install-location /Applications/ --scripts ${installer_build_dir}/update_scripts ${installer_build_dir}/updater.pkg
  pkgbuild --nopayload --identifier com.skycoin.skywire.remover --scripts ${installer_build_dir}/remove_scripts ${installer_build_dir}/remover.pkg

  package_name=SkywireInstaller-${git_tag}-${date_format}-${go_arch}.pkg

  cp ${mac_script_dir}/Distribution_customized.xml ${installer_build_dir}/Distribution.xml

  if [ "$sign_binary" == true ] && [ ! -z ${MAC_HASH_INSTALLER_ID+x} ]; then
    productbuild --sign "$MAC_HASH_INSTALLER_ID" --distribution ${installer_build_dir}/Distribution.xml --package-path ${installer_build_dir} "${output}""${package_name}"
  else
    productbuild --distribution ${installer_build_dir}/Distribution.xml --package-path ${installer_build_dir} "${output}""${package_name}"
  fi

  cd "${output}"

  if [ "$notarize_binary" == true ]; then
    if [ -z "$MAC_DEVELOPER_USERNAME" ] || [ -z "$MAC_DEVELOPER_PASSWORD" ]; then
      echo -e "${yellowt}environment variables: ${greent}MAC_DEVELOPER_USERNAME${nct}${yellowt} and ${greent}MAC_DEVELOPER_PASSWORD${nct}${yellowt} has to be set first before you can notarize the binary."
    fi
    xcrun altool --notarize-app --primary-bundle-id "com.skycoin.skywire" --username="$MAC_DEVELOPER_USERNAME" --password="$MAC_DEVELOPER_PASSWORD" --file "${output}""${package_name}" && {
      echo -e "${greent}check your email for notarization status${nct}"
    }
  fi
}

while :; do
  case "$1" in
  -o | --output)
    if [ -n "$2" ]; then
      output=$2
      shift
    else
      printf 'ERROR: "--output" requires a non-empty option argument.\n' >&2
      exit 1
    fi
    ;;
  --output=?*)
    output=${1#*=}
    ;;
  --output=)
    printf 'ERROR: "--output" requires a non-empty option argument.\n' >&2
    exit 1
    ;;
  -h | --help)
    print_usage
    exit 0
    ;;
  -s | --sign)
    sign_binary=true
    shift
    ;;
  -n | --notarize)
    notarize_binary=true
    shift
    ;;
  -t | --staple)
    staple_notarization=true
    package_path="${2}"
    shift
    ;;
  -?*)
    printf 'WARN: Unknown option (ignored): %s\n' "$1" >&2
    ;;
  *)
    break
    ;;
  esac
  shift
done

# call build_installer twice, once for the original host architecture
# and one for the other one (x86_64 and arm64 or vice versa)

if [ "$staple_notarization" == false ]; then
  build_installer

  case ${go_arch} in
  amd64)
    go_arch=arm64
    build_installer
    ;;
  arm64)
    go_arch=amd64
    build_installer
    ;;
  esac
else
  staple_installer
fi
