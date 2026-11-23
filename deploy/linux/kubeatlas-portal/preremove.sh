#!/bin/sh
set -eu

case "${1-}" in
    remove|0) ;;
    *) exit 0 ;;
esac
if [ "${KUBEATLAS_NATIVE_INSTALL_MANAGED-}" = 1 ] ||
    [ -e /etc/systemd/system/kubeatlas-portal.service ] || [ -L /etc/systemd/system/kubeatlas-portal.service ] ||
    [ -e /var/lib/kubeatlas-portal-install/deployment.json ]; then
    exit 0
fi

if [ -d /run/systemd/system ]; then
    if [ "$1" = remove ]; then
        deb-systemd-invoke stop kubeatlas-portal.service
    else
        systemctl disable --now kubeatlas-portal.service
    fi
fi
