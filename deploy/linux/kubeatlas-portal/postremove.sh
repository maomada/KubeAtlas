#!/bin/sh
set -eu

case "${1-}" in
    remove|purge|0) ;;
    *) exit 0 ;;
esac
if [ "${KUBEATLAS_NATIVE_INSTALL_MANAGED-}" = 1 ] ||
    [ -e /etc/systemd/system/kubeatlas-portal.service ] || [ -L /etc/systemd/system/kubeatlas-portal.service ] ||
    [ -e /var/lib/kubeatlas-portal-install/deployment.json ]; then
    exit 0
fi

if [ "$1" = purge ] && command -v deb-systemd-helper >/dev/null; then
    deb-systemd-helper purge kubeatlas-portal.service >/dev/null
fi
if [ -d /run/systemd/system ]; then
    systemctl daemon-reload
fi
