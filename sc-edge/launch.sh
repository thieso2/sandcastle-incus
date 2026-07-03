#!/usr/bin/env bash
#
# Provision sc-edge as an Incus SYSTEM container (CT).
#
# sc-edge is one appliance / two cooperating processes: Caddy (the routing brain,
# owns all three ingress modes - SNI passthrough, ACME terminate, Cloudflare
# tunnel) and cloudflared (a dumb outbound pipe, installed only when a tunnel
# token is given). Both run natively under systemd inside the CT (no Docker). The
# custom caddy-l4 (layer4) binary is pulled from Caddy's official download API, so
# no Go/xcaddy toolchain is needed inside the container.
#
# The Caddyfile in this directory is the single source of truth: it is pushed to
# /etc/caddy/Caddyfile. Re-run `incus file push Caddyfile <name>/etc/caddy/Caddyfile`
# and `incus exec <name> -- systemctl reload caddy` to apply later edits.
#
# Usage:
#   ./launch.sh [name]
# Env:
#   IMAGE                    base image (default: images:debian/13)
#   ACME_EMAIL               Let's Encrypt contact (default: you@example.com)
#   DATA_HOST_PATH           optional host dir to back /var/lib/caddy so issued
#                            certs survive deleting/recreating the CT (rootfs
#                            already persists across restarts without this).
#   CLOUDFLARE_TUNNEL_TOKEN  if set, install + enable cloudflared for the
#                            Cloudflare tunnel mode. The tunnel is remotely
#                            managed: create it (and a wildcard *.domain ->
#                            http://127.0.0.1:8080 route) once in the Cloudflare
#                            dashboard, then paste its token here. Unset = no
#                            tunnel (public-IP modes only).
#   PUBLIC_PORTS             publish host :80/:443 into the CT (default: 1).
#                            Set to 0 on a host with NO public IP that serves
#                            apps only via the Cloudflare tunnel.
set -euo pipefail

NAME=${1:-sc-edge}
IMAGE=${IMAGE:-images:debian/13}
ACME_EMAIL=${ACME_EMAIL:-you@example.com}
DATA_HOST_PATH=${DATA_HOST_PATH:-}
CLOUDFLARE_TUNNEL_TOKEN=${CLOUDFLARE_TUNNEL_TOKEN:-}
PUBLIC_PORTS=${PUBLIC_PORTS:-1}
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
# Fetch the binary on the HOST (fast) and push it in. An in-container download
# over the NAT'd bridge can crawl or time out entirely on some hosts (observed:
# a 35 MB fetch timing out past 10 min in-container vs <1 s on the host). Same
# host-fetch-then-push trick for cloudflared below.
ARCH=$(dpkg --print-architecture)
CADDY_TMP=$(mktemp)
curl -fsSL -o "$CADDY_TMP" "https://caddyserver.com/api/download?os=linux&arch=${ARCH}&p=github.com/mholt/caddy-l4"
incus file push "$CADDY_TMP" "$NAME"/usr/bin/caddy --mode 0755
rm -f "$CADDY_TMP"
incus exec "$NAME" -- sh -eu -c '
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

# ---- Cloudflare tunnel (mode 3) - only when a token is provided --------------
if [ -n "$CLOUDFLARE_TUNNEL_TOKEN" ]; then
	echo ">> installing cloudflared (Cloudflare tunnel enabled)"
	CF_TMP=$(mktemp)
	curl -fsSL -o "$CF_TMP" \
		"https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-${ARCH}"
	incus file push "$CF_TMP" "$NAME"/usr/bin/cloudflared --mode 0755
	rm -f "$CF_TMP"
	echo ">> pushing cloudflared unit + token"
	incus file push "$HERE/cloudflared.service" "$NAME"/etc/systemd/system/cloudflared.service
	incus exec "$NAME" -- sh -c "printf 'TUNNEL_TOKEN=%s\n' '$CLOUDFLARE_TUNNEL_TOKEN' > /etc/default/cloudflared"
	incus exec "$NAME" -- chmod 600 /etc/default/cloudflared
	incus exec "$NAME" -- systemctl daemon-reload
	incus exec "$NAME" -- systemctl enable --now cloudflared
else
	echo ">> no CLOUDFLARE_TUNNEL_TOKEN set - skipping cloudflared (public-IP modes only)"
fi

# ---- publish host :80/:443 (public-IP modes) - skip on IP-less tunnel hosts ---
if [ "$PUBLIC_PORTS" != "0" ]; then
	echo ">> publishing host :80 and :443 into the CT"
	incus config device add "$NAME" http  proxy listen=tcp:0.0.0.0:80  connect=tcp:127.0.0.1:80
	incus config device add "$NAME" https proxy listen=tcp:0.0.0.0:443 connect=tcp:127.0.0.1:443
else
	echo ">> PUBLIC_PORTS=0 - not publishing :80/:443 (tunnel-only host)"
fi

echo ">> done. edit ./Caddyfile then:"
echo "     incus file push $HERE/Caddyfile ${NAME}/etc/caddy/Caddyfile && incus exec ${NAME} -- systemctl reload caddy"
