#!/bin/sh
set -eu

case "${1-}" in
    configure)
        package_manager=deb
        if [ -z "${2-}" ]; then fresh=yes; else fresh=no; fi
        ;;
    abort-upgrade|abort-remove)
        package_manager=deb
        fresh=no
        ;;
    1) package_manager=rpm; fresh=yes ;;
    [1-9]*) package_manager=rpm; fresh=no ;;
    *) exit 0 ;;
esac

if [ "${KUBEATLAS_NATIVE_INSTALL_MANAGED-}" = 1 ] ||
    [ -e /etc/systemd/system/kubeatlas-portal.service ] || [ -L /etc/systemd/system/kubeatlas-portal.service ] ||
    [ -e /var/lib/kubeatlas-portal-install/deployment.json ]; then
    exit 0
fi

service=kubeatlas-portal
state=/var/lib/$service
if ! getent passwd "$service" >/dev/null; then
    nologin=/usr/sbin/nologin
    if [ ! -x "$nologin" ]; then nologin=/sbin/nologin; fi
    useradd --system --user-group --no-create-home --home-dir "$state" --shell "$nologin" "$service"
fi
entry=$(getent passwd "$service")
group=$(getent group "$service")
uid=$(printf '%s\n' "$entry" | cut -d: -f3)
gid=$(printf '%s\n' "$group" | cut -d: -f3)
shell=${entry##*:}
if [ "$uid" = 0 ] || [ "$gid" = 0 ] || [ "${shell##*/}" != nologin ] ||
    [ "$(printf '%s\n' "$entry" | cut -d: -f4,6)" != "$gid:$state" ]; then
    printf 'KubeAtlas Hub: The existing service account has incompatible permissions.\n' >&2
    exit 1
fi

# Keep package installation usable in images and native extraction checks.
if [ "${1-}" = configure ] || [ "$package_manager" = rpm ]; then
    /usr/bin/kubeatlas-portal --check-hub-artifacts --hub-artifacts-dir=/usr/share/kubeatlas-portal/agent-catalog
fi
if [ "$fresh" = yes ] && [ "$package_manager" = deb ]; then
    deb-systemd-helper enable "$service.service" >/dev/null
fi
[ -d /run/systemd/system ] || exit 0

systemctl daemon-reload
if [ "$fresh" = yes ]; then
    if [ "$package_manager" = deb ]; then
        deb-systemd-invoke start "$service.service"
    else
        systemctl enable --now "$service.service"
    fi
elif systemctl is-active --quiet "$service.service"; then
    if [ "$package_manager" = deb ]; then
        deb-systemd-invoke restart "$service.service"
    else
        systemctl restart "$service.service"
    fi
fi
