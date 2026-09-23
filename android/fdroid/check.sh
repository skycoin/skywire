#!/usr/bin/env bash
# Runs android/fdroid/com.skycoin.skywire.yml the way F-Droid will:
#
#   1. fdroid lint          — what fdroiddata's CI checks on the merge request
#   2. fdroid checkupdates  — what F-Droid's update bot runs to find a new
#                             fdroid-v* tag and add its build by itself
#   3. fdroid build         — what F-Droid's build server runs, scanner included
#
# for the commit checked out at $SRC — the release's commit with its version
# stamped into android/version.properties. Runs as root inside F-Droid's own
# build server image; .github/workflows/android-fdroid.yml starts it like this
# after each mobile-v* release (on a GitHub runner — the image is several GB):
#
#   docker run --rm -v "$PWD:/src" -v "$PWD/fdroid-out:/out" \
#     -v "$CI_DIR:/ci:ro" -e RECIPE=/ci/android/fdroid/com.skycoin.skywire.yml \
#     registry.gitlab.com/fdroid/fdroidserver:buildserver-trixie \
#     bash /ci/android/fdroid/check.sh
#
# where $CI_DIR holds android/fdroid from the branch the workflow runs from.
#
# The build half follows the "fdroid build" job of fdroiddata's .gitlab-ci.yml.
# The unsigned APK F-Droid would sign lands in $OUT.
set -euo pipefail

APPID=com.skycoin.skywire
SRC=${SRC:-/src}
OUT=${OUT:-/out}
# The recipe to run: the tag's own copy unless the caller passes a newer one
# (a manual re-run of an old tag uses develop's, see android-fdroid.yml).
RECIPE=${RECIPE:-$SRC/android/fdroid/$APPID.yml}

step() { printf '\n\033[1m==> %s\033[0m\n' "$*"; }
fail() { printf '\033[31mFAIL: %s\033[0m\n' "$*" >&2; exit 1; }

# home_vagrant, fdroidserver, ANDROID_HOME — the build server's environment.
set +u
# shellcheck disable=SC1091
source /etc/profile.d/bsenv.sh
set -u
# shellcheck disable=SC2154
: "${home_vagrant:?not the F-Droid build server image}" "${fdroidserver:?}" "${ANDROID_HOME:?}"

step "fdroidserver master, as fdroiddata's CI and the update bot run it"
apt-get -qq update
apt-get -qqy install sudo git curl openjdk-21-jdk-headless >/dev/null
update-alternatives --set java /usr/lib/jvm/java-21-openjdk-amd64/bin/java
rm -rf "$fdroidserver"
mkdir -p "$fdroidserver"
curl -fsSL https://gitlab.com/fdroid/fdroidserver/-/archive/master/fdroidserver-master.tar.gz \
  | tar -xz --directory="$fdroidserver" --strip-components=1
git -C "$home_vagrant/gradlew-fdroid" pull -q || true
# The source repo belongs to whoever ran docker; the builds run as vagrant.
git config --system --add safe.directory '*'
fdroid() {
  sudo --preserve-env --user vagrant \
    env PATH="$fdroidserver:$PATH" \
    PYTHONPATH="$fdroidserver:$fdroidserver/examples" \
    PYTHONUNBUFFERED=true HOME="$home_vagrant" \
    "$fdroidserver/fdroid" "$@"
}

name=$(sed -n 's/^versionName=//p' "$SRC/android/version.properties")
code=$(sed -n 's/^versionCode=//p' "$SRC/android/version.properties")
head_sha=$(git -C "$SRC" rev-parse HEAD)
[ -n "$name" ] && [ -n "$code" ] || fail "android/version.properties has no versionName/versionCode"
echo "checking $APPID $name ($code) at $head_sha"

cd "$home_vagrant"
mkdir -p metadata logs tmp unsigned
cp "$RECIPE" "metadata/$APPID.yml"
# lint checks Categories against fdroiddata's own list, which lives in its
# config/, not in fdroidserver. The icon lines point at files in fdroiddata.
mkdir -p config
curl -fsSL https://gitlab.com/fdroid/fdroiddata/-/raw/master/config/categories.yml \
  | sed '/^ *icon:/d' > config/categories.yml
chown -R vagrant "$home_vagrant"

step "fdroid lint (the recipe exactly as it is submitted)"
fdroid lint "$APPID"

# From here the recipe clones a local copy of this checkout instead of
# GitHub, so it builds this commit. The fdroid-v tag the bot looks for is only
# pushed once this script has passed, so it is made here, in a bare copy that
# shares objects with $SRC and never touches it.
repo=/tmp/skywire.git
rm -rf "$repo"
git clone -q --bare --shared "$SRC" "$repo"
git -C "$repo" branch -f fdroid-check "$head_sha"
# $SRC is on a detached HEAD, so the copy's HEAD names no branch; point it at
# this commit so a clone of the copy checks something out.
git -C "$repo" symbolic-ref HEAD refs/heads/fdroid-check
git -C "$repo" tag -f "fdroid-v$name" "$head_sha"
sed -i "s|^Repo: .*|Repo: $repo|" "metadata/$APPID.yml"
chown -R vagrant "$home_vagrant"

step "fdroid checkupdates (the update bot: finds fdroid-v$name, reads its version)"
# --allow-dirty: checkupdates otherwise insists on running inside the
# fdroiddata git repo and checks it has no uncommitted changes.
fdroid checkupdates --allow-dirty --auto --verbose "$APPID"
grep -q "^CurrentVersionCode: $code\$" "metadata/$APPID.yml" \
  || fail "the update bot did not pick up $name ($code) from android/version.properties at fdroid-v$name"
grep -q "^    versionCode: $code\$" "metadata/$APPID.yml" \
  || fail "no build entry for versionCode $code after checkupdates"
echo "update bot sees $name ($code)"

# Build this commit even if an fdroid-v$name from an earlier run already
# points at another one.
python3 - "metadata/$APPID.yml" "$code" "$head_sha" <<'EOF'
import sys
path, code, sha = sys.argv[1], sys.argv[2], sys.argv[3]
lines = open(path).read().split("\n")
in_entry = False
for i, line in enumerate(lines):
    if line.startswith("  - "):
        in_entry = False
    if line == f"    versionCode: {code}":
        in_entry = True
    if in_entry and line.startswith("    commit: "):
        lines[i] = f"    commit: {sha}"
        break
else:
    sys.exit(f"no commit line for versionCode {code}")
open(path, "w").write("\n".join(lines))
EOF

step "fdroid build $APPID:$code (the build server, scanner included)"
# fdroiddata's CI unsets CI for the build too.
(unset CI; fdroid build --verbose --test --refresh-scanner --on-server --no-tarball "$APPID:$code")

apk="tmp/${APPID}_${code}.apk"
[ -f "$apk" ] || fail "fdroid build produced no $apk"

step "the F-Droid APK carries no self-updater"
aapt2=$(find "$ANDROID_HOME/build-tools" -name aapt2 -type f | sort -V | tail -n1)
[ -n "$aapt2" ] || fail "aapt2 not found under $ANDROID_HOME/build-tools"
if "$aapt2" dump permissions "$apk" | grep -q REQUEST_INSTALL_PACKAGES; then
  fail "$apk still asks for REQUEST_INSTALL_PACKAGES — skywireFdroid did not apply"
fi
"$aapt2" dump badging "$apk" | grep -E "^package:"

mkdir -p "$OUT"
cp "$apk" "$OUT/"
echo "OK: $OUT/$(basename "$apk")"
