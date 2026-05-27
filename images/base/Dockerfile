FROM debian:13-slim

ARG COREDNS_VERSION=1.14.3
ARG SANDCASTLE_IMAGE_TEMPLATE=base
ARG SANDCASTLE_IMAGE_TAG=sandcastle/base:latest
ARG SANDCASTLE_IMAGE_VERSION=unknown
ARG SANDCASTLE_IMAGE_COMMIT_DATE=unknown
ARG TARGETARCH

ENV DEBIAN_FRONTEND=noninteractive

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        bash \
        ca-certificates \
        caddy \
        curl \
        dbus \
        dialog \
        docker-cli \
        docker.io \
        ethtool \
        gnupg \
        iproute2 \
        iputils-ping \
        locales \
        lsof \
        mosh \
        netcat-openbsd \
        net-tools \
        openssh-server \
        python3 \
        sudo \
        systemd \
        systemd-sysv \
        tar \
        tmux \
        traceroute \
        vim \
    && rm -rf /var/lib/apt/lists/*

RUN systemctl disable docker.service docker.socket containerd.service >/dev/null 2>&1 || true \
    && rm -f \
        /etc/systemd/system/multi-user.target.wants/docker.service \
        /etc/systemd/system/multi-user.target.wants/containerd.service \
        /etc/systemd/system/sockets.target.wants/docker.socket

RUN sed -i 's/^# *\(de_DE.UTF-8 UTF-8\)/\1/' /etc/locale.gen \
    && sed -i 's/^# *\(en_US.UTF-8 UTF-8\)/\1/' /etc/locale.gen \
    && locale-gen \
    && update-locale LANG=C.UTF-8

RUN printf 'Sandcastle %s image\nVersion: %s\nImage: %s\nLast commit: %s\n\n' \
        "$SANDCASTLE_IMAGE_TEMPLATE" \
        "$SANDCASTLE_IMAGE_VERSION" \
        "$SANDCASTLE_IMAGE_TAG" \
        "$SANDCASTLE_IMAGE_COMMIT_DATE" \
        >/etc/issue \
    && cp /etc/issue /etc/issue.net

RUN set -eux; \
    arch="${TARGETARCH:-amd64}"; \
    case "${arch}" in \
        amd64) coredns_arch="amd64" ;; \
        arm64) coredns_arch="arm64" ;; \
        arm) coredns_arch="arm" ;; \
        *) echo "unsupported CoreDNS architecture: ${arch}" >&2; exit 1 ;; \
    esac; \
    curl -fsSL "https://github.com/coredns/coredns/releases/download/v${COREDNS_VERSION}/coredns_${COREDNS_VERSION}_linux_${coredns_arch}.tgz" \
        -o /tmp/coredns.tgz; \
    tar -xzf /tmp/coredns.tgz -C /usr/local/bin coredns; \
    chmod 0755 /usr/local/bin/coredns; \
    rm -f /tmp/coredns.tgz

RUN install -d -m 0755 /usr/share/keyrings \
    && curl -fsSL https://packages.cloud.google.com/apt/doc/apt-key.gpg \
        | gpg --dearmor -o /usr/share/keyrings/cloud.google.gpg \
    && echo "deb [signed-by=/usr/share/keyrings/cloud.google.gpg] https://packages.cloud.google.com/apt cloud-sdk main" \
        >/etc/apt/sources.list.d/google-cloud-sdk.list \
    && apt-get update \
    && apt-get install -y --no-install-recommends google-cloud-cli \
    && rm -rf /var/lib/apt/lists/*

RUN install -d -m 0755 /usr/share/keyrings \
    && curl -fsSL https://pkgs.tailscale.com/stable/debian/trixie.noarmor.gpg \
        -o /usr/share/keyrings/tailscale-archive-keyring.gpg \
    && curl -fsSL https://pkgs.tailscale.com/stable/debian/trixie.tailscale-keyring.list \
        -o /etc/apt/sources.list.d/tailscale.list \
    && apt-get update \
    && apt-get install -y --no-install-recommends tailscale \
    && rm -rf /var/lib/apt/lists/*

RUN systemctl disable tailscaled.service >/dev/null 2>&1 || true \
    && systemctl mask tailscaled.service >/dev/null 2>&1 || true \
    && rm -f /etc/systemd/system/multi-user.target.wants/tailscaled.service

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
