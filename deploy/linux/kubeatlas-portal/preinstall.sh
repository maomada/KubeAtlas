#!/bin/sh
set -eu

fail() {
    printf 'KubeAtlas Hub: %s\n' "$*" >&2
    exit 1
}

case "${1-}" in
    install|upgrade|[1-9]*) ;;
    *) exit 0 ;;
esac

# The verified installer manages systemd, state, catalogs and rollback itself.
if [ "${KUBEATLAS_NATIVE_INSTALL_MANAGED-}" = 1 ]; then
    lock=/run/lock/kubeatlas-portal-install.lock
    [ -f "$lock" ] && [ ! -L "$lock" ] || fail "The managed installer lock is unavailable."
    exec 9<"$lock"
    result=0
    flock -n -E 75 9 || result=$?
    [ "$result" -eq 75 ] || fail "Run the managed installer rather than invoking the package directly."
    exit 0
fi

for unit in /etc/systemd/system/kubeatlas-portal.service /run/systemd/system/kubeatlas-portal.service /usr/local/lib/systemd/system/kubeatlas-portal.service; do
    if [ -e "$unit" ] || [ -L "$unit" ]; then
        fail "An existing custom Hub service was found at $unit. Upgrade it with the managed installer or migrate it explicitly."
    fi
done
if [ -e /var/lib/kubeatlas-portal-install/deployment.json ] || [ -L /var/lib/kubeatlas-portal-install/deployment.json ]; then
    fail "An existing installer-managed Hub was found. Use install-hub.py upgrade."
fi

catalog=/usr/share/kubeatlas-portal/agent-catalog
[ -e "$catalog" ] || [ -L "$catalog" ] || exit 0
binary=/usr/bin/kubeatlas-portal
version_line=$("$binary" --version)
version=${version_line#kubeatlas-portal }
if [ "$version" = "$version_line" ] ||
    ! printf '%s\n' "$version" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$'; then
    fail "Cannot identify the installed Hub release."
fi

# Debian exposes both versions on upgrade. A reinstall can reuse its catalog.
if [ "${1-}" = upgrade ] && [ -n "${2-}" ] && [ "${2-}" = "${3-}" ]; then
    "$binary" --check-hub-artifacts "--hub-artifacts-dir=$catalog"
    exit 0
fi

retained=/var/lib/kubeatlas-portal-native
previous=$retained/previous
if [ ! -e "$retained" ] && [ ! -L "$retained" ]; then
    install -d -m 0755 "$retained"
fi
[ -d "$retained" ] && [ ! -L "$retained" ] && [ "$(stat -c '%u:%g:%a' "$retained")" = 0:0:755 ] ||
    fail "The Hub catalog retention directory must be root-owned with mode 0755."
if [ -e "$previous" ] || [ -L "$previous" ]; then
    if [ ! -d "$previous" ] || [ -L "$previous" ] ||
        [ ! -f "$previous/catalog.env" ] || [ -L "$previous/catalog.env" ] ||
        ! grep -Fxq "KUBEATLAS_HUB_PREVIOUS_VERSION=$version" "$previous/catalog.env"; then
        fail "A previous Agent catalog is retained at $previous. Retire it after migrating its Agents before upgrading again."
    fi
    "$binary" --check-hub-artifacts "--hub-artifacts-dir=$previous/catalog"
    exit 0
fi

umask 077
staged=$(mktemp -d "$retained/.previous.XXXXXX")
trap 'rm -rf -- "$staged"' EXIT
trap 'exit 1' HUP INT TERM
cp -a -- "$catalog" "$staged/catalog"
"$binary" --check-hub-artifacts "--hub-artifacts-dir=$staged/catalog"
printf 'KUBEATLAS_HUB_PREVIOUS_DIR=%s/catalog\nKUBEATLAS_HUB_PREVIOUS_VERSION=%s\n' "$previous" "$version" > "$staged/catalog.env"
chmod 0755 "$staged"
mv -T -- "$staged" "$previous"
trap - EXIT HUP INT TERM
