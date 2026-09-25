#!/usr/bin/env bash
# Signs the APK check.sh built with the release key. The result is the
# reference binary the recipe's Binaries points F-Droid at: F-Droid builds the
# same commit, copies this APK's signature onto its own build and publishes
# this APK only if the signature still verifies there, which it does only if
# the two builds are byte-identical.
#
#   sign.sh UNSIGNED_APK SIGNED_APK [PUBLISHED_APK]
#
# Checks, before anything is published:
#
#   1. the signature is v2+v3 only and apksigner kept the zip layout as it
#      was, so the signed APK is the unsigned one plus a signing block. A v1
#      signature adds META-INF entries, and apksigner's default re-padding
#      rewrites every stored entry; either one leaves a signature that does not
#      fit an unsigned build, not even the one it was made from.
#   2. fdroidserver's own verify_apks passes: the check `fdroid build` runs
#      against Binaries, run here on this lane's build.
#   3. the signer is in the recipe's AllowedAPKSigningKeys.
#   4. PUBLISHED_APK, when given (a re-run finding the asset already on the
#      release), passes 2 against this build as well. A release asset F-Droid
#      may already have checked is never replaced, so a fresh build that does
#      not match it is an error, not an upload.
#
# Runs as root in F-Droid's build server image, like check.sh; the key comes
# in as KEYSTORE (a keystore file) plus ANDROID_KEYSTORE_PASSWORD,
# ANDROID_KEY_ALIAS and ANDROID_KEY_PASSWORD, as in android-release.yml.
# .github/workflows/android-fdroid.yml runs it after check.sh.
set -euo pipefail

UNSIGNED=${1:?unsigned APK}
SIGNED=${2:?signed APK to write}
PUBLISHED=${3:-}
RECIPE=${RECIPE:?the recipe, for AllowedAPKSigningKeys}
: "${KEYSTORE:?}" "${ANDROID_KEYSTORE_PASSWORD:?}" "${ANDROID_KEY_ALIAS:?}" "${ANDROID_KEY_PASSWORD:?}"

step() { printf '\n\033[1m==> %s\033[0m\n' "$*"; }

set +u
# shellcheck disable=SC1091
source /etc/profile.d/bsenv.sh
set -u
# shellcheck disable=SC2154
: "${fdroidserver:?not the F-Droid build server image}" "${ANDROID_HOME:?}"

step "fdroidserver master, as check.sh and F-Droid's build run it"
apt-get -qq update
apt-get -qqy install curl openjdk-21-jdk-headless >/dev/null
update-alternatives --set java /usr/lib/jvm/java-21-openjdk-amd64/bin/java
rm -rf "$fdroidserver"
mkdir -p "$fdroidserver"
curl -fsSL https://gitlab.com/fdroid/fdroidserver/-/archive/master/fdroidserver-master.tar.gz \
  | tar -xz --directory="$fdroidserver" --strip-components=1

# The image has no build-tools: in check.sh's container gradle installs them
# as the build needs them. This is the version the build uses, and the
# apksigner that --alignment-preserved below was checked with.
step "apksigner (Android SDK Build-Tools 36.0.0)"
sdkmanager "build-tools;36.0.0" >/dev/null
apksigner="$ANDROID_HOME/build-tools/36.0.0/apksigner"
[ -x "$apksigner" ] || { echo "no $apksigner after sdkmanager" >&2; exit 1; }

step "sign $(basename "$UNSIGNED") -> $(basename "$SIGNED")"
mkdir -p "$(dirname "$SIGNED")"
"$apksigner" sign \
  --ks "$KEYSTORE" --ks-key-alias "$ANDROID_KEY_ALIAS" \
  --ks-pass env:ANDROID_KEYSTORE_PASSWORD --key-pass env:ANDROID_KEY_PASSWORD \
  --alignment-preserved \
  --v1-signing-enabled false --v2-signing-enabled true --v3-signing-enabled true \
  --v4-signing-enabled false \
  --in "$UNSIGNED" --out "$SIGNED"
"$apksigner" verify --verbose "$SIGNED" | grep -E '^Verifie'

step "verify it the way F-Droid will"
PYTHONPATH="$fdroidserver" python3 - "$SIGNED" "$UNSIGNED" "$RECIPE" "$PUBLISHED" <<'EOF'
import sys, tempfile
import yaml
from fdroidserver import common

signed, unsigned, recipe, published = sys.argv[1:5]
common.config = common.read_config()

allowed = yaml.safe_load(open(recipe)).get("AllowedAPKSigningKeys") or []
if isinstance(allowed, str):
    allowed = [allowed]
allowed = [k.lower() for k in allowed]

for ref in [signed] + ([published] if published else []):
    with tempfile.TemporaryDirectory() as tmp:
        err = common.verify_apks(ref, unsigned, tmp)
    if err:
        sys.exit(f"FAIL: the signature of {ref} does not verify on {unsigned}:\n{err}")
    print(f"OK: the signature of {ref} verifies on {unsigned}")
    signer = common.apk_signer_fingerprint(ref)
    if signer not in allowed:
        sys.exit(f"FAIL: {ref} is signed by {signer}; AllowedAPKSigningKeys is {allowed}")
    print(f"OK: {ref} is signed by {signer}, in AllowedAPKSigningKeys")
EOF
