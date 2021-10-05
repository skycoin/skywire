#!/usr/bin/env bash

# variables
mac_script_dir="./scripts/mac_installer"
installer_build_dir="./mac_build"
installer_package_dir="${installer_build_dir}"/binaries/Skywireapp
git_tag=$(git describe --tags)
date_format=$(date -u "+%Y-%m-%d")
go_arch=$(go env GOARCH) # build for amd64 and arm64 from single host
cert_path=
developer_id=
output=

# Has to be run from MacOS Host
current_os="$(uname -s)"
if [[ "$current_os" != "Darwin" ]]; then
  echo "Can only be run from MacOS Host"
  exit 1
fi

function print_usage() {
  echo "Usage: sh create_installer.sh [-o|--output output_skywire_dir] [-d|--dev-id developer_id] [-c | --cert CERTIFICATE in p12 format]"
  echo "You need to provide --dev-id / -d and --cert / -c options if you want to sign the binary"
  echo "You also need to set these environment variables, if you want to sign the binary:"
  echo "MAC_APP_KEYCHAIN_PASSWORD    : Keychain password for skywireBuild keychain"
  echo "MAC_APP_CERTIFICATE_PASSWORD : Password of your p12 Developer Application Certificate"
  echo "MAC_APP_DEV_USERNAME              : Apple Developer account email"
  echo "MAC_APP_DEV_PASSWORD              : Apple Developer account password"
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
  
    test -x /usr/local/bin/gon || {
      brew tap mitchellh/gon
      brew install mitchellh/gon/gon
    }
  
    # build skywire binariea
    make CGO_ENABLED=1 GOOS=darwin GOARCH="${go_arch}" build-systray
  
    # Create directories
    mkdir -p ${installer_build_dir}/binaries/Skywireapp
    mkdir -p ${installer_package_dir}/Contents/{Resources,MacOS/apps}
  
    # build deinstaller
    go build -o ${installer_package_dir}/Contents/deinstaller ${mac_script_dir}/desktop-deinstaller
  
    # prepare Distribution.xml
    cp ${mac_script_dir}/Distribution.xml ${installer_build_dir}/
  
    # modify version info
    cp ${mac_script_dir}/AppInfo.plist.tmpl ${installer_package_dir}/Contents/Info.plist
    perl -i -pe "s/{{BundleVersion}}/${git_tag}/g" ${installer_package_dir}/Contents/Info.plist
  
    cp ${mac_script_dir}/icon.icns ${installer_package_dir}/Contents/Resources/icon.icns
    mv ./skywire-visor ${installer_package_dir}/Contents/MacOS/skywire-visor
    mv ./skywire-cli ${installer_package_dir}/Contents/MacOS/skywire-cli
    mv ./apps/vpn-client ${installer_package_dir}/Contents/MacOS/apps/vpn-client
  
    cat <<EOF >${installer_package_dir}/Contents/MacOS/Skywire
  #!/usr/bin/env bash
  
  
  osascript -e "do shell script \"/Applications/Skywire.app/Contents/MacOS/skywire-visor --systray >> /Users/\${USER}/Library/Logs/skywire/visor.log\" with administrator privileges"
  #/Applications/Skywire.app/Contents/MacOS/skywire-visor --systray >> /Users/\${USER}/Library/Logs/skywire/visor.log
  
EOF
  
    chmod +x ${installer_package_dir}/Contents/MacOS/Skywire
  
    # prepare install scripts
    mkdir -p ${installer_build_dir}/{install,update,remove}_scripts
    cp -Rv ${mac_script_dir}/install_scripts/* ${installer_build_dir}/install_scripts/
    cp -Rv ${mac_script_dir}/update_scripts/* ${installer_build_dir}/update_scripts/
    cp -Rv ${mac_script_dir}/remove_scripts/* ${installer_build_dir}/remove_scripts/
  
    # build installer
    pkgbuild --root ${installer_build_dir}/binaries --identifier com.skycoin.skywire.visor --install-location /tmp/skywire --scripts ${installer_build_dir}/install_scripts ${installer_build_dir}/installer.pkg
    pkgbuild --root ${installer_build_dir}/binaries --identifier com.skycoin.skywire.updater --install-location /tmp/skywire --scripts ${installer_build_dir}/update_scripts ${installer_build_dir}/updater.pkg
    pkgbuild --nopayload --identifier com.skycoin.skywire.remover --scripts ${installer_build_dir}/remove_scripts ${installer_build_dir}/remover.pkg
  
  package_name=SkywireInstaller-${git_tag}-${date_format}-${go_arch}.pkg
  
    cp ${mac_script_dir}/Distribution_customized.xml ${installer_build_dir}/Distribution.xml
    productbuild --distribution ${installer_build_dir}/Distribution.xml --package-path ${installer_build_dir} "${output}""${package_name}"

  cd "${output}"

  if [ -n "$developer_id" ] && [[ "$developer_id" != "" ]]; then
    if [ -n "$cert_path" ] && [[ "$cert_path" != "" ]]; then
      test -f "$cert_path" || {
        echo "No valid certificate found at this path..."
        exit 0
      }
    fi

    echo "Creating keychain and importing your certificate"
    security create-keychain -p "$MAC_APP_KEYCHAIN_PASSWORD" skywireBuild.keychain || echo "already created"
    security list-keychains -d user -s login.keychain skywireBuild.keychain

    echo "no certificate exists yet, importing..."
    security import "$cert_path" -k skywireBuild.keychain -P "$MAC_APP_CERTIFICATE_PASSWORD" || echo "already imported"

    dmg_name=skywire-${git_tag}-${date_format}-${go_arch}.dmg
    # create gon config
    cat <<EOF >"${output}/package-signing-config.json"
    {
        "source" : ["./$package_name"],
        "bundle_id" : "com.skycoin.skywire",
        "apple_id": {
            "username" : "$MAC_APP_DEV_USERNAME",
            "password":  "$MAC_APP_DEV_PASSWORD"
        },
        "sign" :{
            "application_identity" : "$developer_id"
        },
        "dmg" :{
            "output_path":  "$dmg_name",
            "volume_name":  "Skywire"
        }
    }
EOF

    # use gon to sign the binary
    gon -log-level=debug -log-json ./package-signing-config.json

    security delete-keychain skywireBuild.keychain
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
    ;;
  -d | --dev-id)
    developer_id="$2"
    shift
    ;;
  -c | --cert)
    cert_path="$2"
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
