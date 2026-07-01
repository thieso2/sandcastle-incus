#!/usr/bin/env bash
#
# Provision caddy-sni-proxy as an Incus SYSTEM container (CT).
#
# Caddy runs natively under systemd inside the CT (no Docker). The custom
# caddy-l4 (layer4) binary is pulled from Caddy's official download API, so no
# Go/xcaddy toolchain is needed inside the container.
#
# The Caddyfile in this directory is the single source of truth: it is pushed to
# /etc/caddy/Caddyfile. Re-run `incus file push Caddyfile <name>/etc/caddy/Caddyfile`
# and `incus exec <name> -- systemctl reload caddy` to apply later edits.
#
# Usage:
#   ./launch.sh [name]
# Env:
#   IMAGE          base image (default: images:debian/13)
#   ACME_EMAIL     Let's Encrypt contact (default: you@example.com)
#   DATA_HOST_PATH optional host dir to back /var/lib/caddy so issued certs
#                  survive deleting/recreating the CT (rootfs already persists
#                  across restarts without this).
set -euo pipefail

NAME=${1:-caddy-edge}
IMAGE=${IMAGE:-images:debian/13}
ACME_EMAIL=${ACME_EMAIL:-you@example.com}
DATA_HOST_PATH=${DATA_HOST_PATH:-}
HERE=$(cd "$(dirname "$0")" && pwd)

echo ">> launching CT ${NAME} from ${IMAGE}"
incus launch "$IMAGE" "$NAME"

# Persist certs on a host path (survives CT deletion). Mount BEFORE Caddy's
# home/data dir is created so nothing is shadowed later.
if [ -n "$DATA_HOST_PATH" ]; then
	mkdir -p "$DATA_HOST_PATH"
	echo ">> backing /var/lib/caddy with host path ${DATA_HOST_PATH}"
	incus config device add "$NAME" data disk source="$DATA_HOST_PATH" path=/var/lib/caddy
fi

echo ">> waiting for network"
incus exec "$NAME" -- sh -c 'for i in $(seq 1 60); do getent hosts caddyserver.com >/dev/null 2>&1 && exit 0; sleep 1; done; echo "no network" >&2; exit 1'

echo ">> installing layer4-enabled caddy binary + caddy user"
incus exec "$NAME" -- sh -eu -c '
	export DEBIAN_FRONTEND=noninteractive
	apt-get update -qq
	apt-get install -y -qq curl ca-certificates >/dev/null
	ARCH=$(dpkg --print-architecture)
	curl -fsSL -o /usr/bin/caddy "https://caddyserver.com/api/download?os=linux&arch=${ARCH}&p=github.com/mholt/caddy-l4"
	chmod +x /usr/bin/caddy
	id caddy >/dev/null 2>&1 || useradd --system --home /var/lib/caddy --create-home --shell /usr/sbin/nologin caddy
	install -d -o caddy -g caddy /var/lib/caddy
	install -d /etc/caddy
'

echo ">> pushing systemd unit, env, and Caddyfile"
incus file push "$HERE/caddy.service" "$NAME"/etc/systemd/system/caddy.service
incus exec "$NAME" -- sh -c "printf 'ACME_EMAIL=%s\n' '$ACME_EMAIL' > /etc/default/caddy"
incus file push "$HERE/Caddyfile" "$NAME"/etc/caddy/Caddyfile

echo ">> validating config"
incus exec "$NAME" -- caddy validate --config /etc/caddy/Caddyfile

echo ">> starting caddy"
incus exec "$NAME" -- systemctl daemon-reload
incus exec "$NAME" -- systemctl enable --now caddy

echo ">> publishing host :80 and :443 into the CT"
incus config device add "$NAME" http  proxy listen=tcp:0.0.0.0:80  connect=tcp:127.0.0.1:80
incus config device add "$NAME" https proxy listen=tcp:0.0.0.0:443 connect=tcp:127.0.0.1:443

echo ">> done. edit ./Caddyfile then:"
echo "     incus file push $HERE/Caddyfile ${NAME}/etc/caddy/Caddyfile && incus exec ${NAME} -- systemctl reload caddy"
