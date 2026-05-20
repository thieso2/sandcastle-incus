FROM debian:13-slim

ENV DEBIAN_FRONTEND=noninteractive

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        bash \
        ca-certificates \
        caddy \
        curl \
        openssh-server \
        sudo \
    && rm -rf /var/lib/apt/lists/*

RUN install -d -m 0755 /usr/share/keyrings \
    && curl -fsSL https://pkgs.tailscale.com/stable/debian/trixie.noarmor.gpg \
        -o /usr/share/keyrings/tailscale-archive-keyring.gpg \
    && curl -fsSL https://pkgs.tailscale.com/stable/debian/trixie.tailscale-keyring.list \
        -o /etc/apt/sources.list.d/tailscale.list \
    && apt-get update \
    && apt-get install -y --no-install-recommends tailscale \
    && rm -rf /var/lib/apt/lists/*

RUN printf 'net.ipv4.ip_forward = 1\nnet.ipv6.conf.all.forwarding = 1\n' >/etc/sysctl.d/99-sandcastle-tailscale.conf

RUN install -d \
        /etc/sandcastle/caddy \
        /run/tailscale \
        /var/lib/sandcastle \
        /var/lib/tailscale \
        /workspace \
        /run/sshd

COPY sandcastle-bootstrap /usr/local/bin/sandcastle-bootstrap
RUN chmod 0755 /usr/local/bin/sandcastle-bootstrap

WORKDIR /workspace
CMD ["sleep", "infinity"]
