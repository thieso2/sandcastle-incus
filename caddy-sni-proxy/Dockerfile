# ---- build stage: compile Caddy with the layer4 (caddy-l4) plugin -------
FROM caddy:2-builder AS build
RUN xcaddy build \
    --with github.com/mholt/caddy-l4

# ---- runtime ------------------------------------------------------------
FROM caddy:2
COPY --from=build /usr/bin/caddy /usr/bin/caddy
COPY Caddyfile /etc/caddy/Caddyfile

# /data holds ACME certs/keys + OCSP staples - persist it across restarts
# so you don't re-issue certs (and risk Let's Encrypt rate limits) on every
# recreate. The base caddy image already declares these; we re-declare /data
# explicitly so the persistence requirement is obvious and survives any base
# change. Callers MUST back /data with a named volume / persistent mount.
VOLUME /data /config
EXPOSE 80 443

# The base image's default command already runs:
#   caddy run --config /etc/caddy/Caddyfile --adapter caddyfile
