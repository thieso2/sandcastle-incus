FROM debian:13-slim

ARG COREDNS_VERSION=1.14.3
ARG TARGETARCH

ENV DEBIAN_FRONTEND=noninteractive

# NOTE: The per-build version args are declared near the end, just before the
# RUN that consumes them. buildah invalidates a layer's cache when any in-scope
# build-arg VALUE changes (even if the layer does not reference it), so keeping
# these out of scope of the heavy package layers lets those layers stay cached
# across commits.

# apt cache mounts keep downloaded .debs and package lists on the builder cache
# volume (not in the image layer), so a rebuild of this heavy layer reuses the
# local archive instead of re-fetching. docker-clean is removed once here so apt
# keeps its archives; its absence persists into all later layers.
RUN --mount=type=cache,target=/var/cache/apt,sharing=locked \
    --mount=type=cache,target=/var/lib/apt/lists,sharing=locked \
    rm -f /etc/apt/apt.conf.d/docker-clean \
    && apt-get update \
    && apt-get install -y --no-install-recommends \
        bash \
        ca-certificates \
        caddy \
        chromium \
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
        openbox \
        openssh-server \
        pyhoca-cli \
        python3 \
        ripgrep \
        samba \
        sudo \
        systemd \
        systemd-sysv \
        tar \
        tmux \
        traceroute \
        vim \
        x2goserver \
        x2goserver-x2gokdrive \
        x2goserver-xsession \
        xfce4-terminal \
        xserver-x2gokdrive

RUN --mount=type=cache,target=/var/cache/apt,sharing=locked \
    --mount=type=cache,target=/var/lib/apt/lists,sharing=locked \
    install -dm 0755 /etc/apt/keyrings \
    && curl -fsSL https://mise.jdx.dev/gpg-key.pub | gpg --dearmor -o /etc/apt/keyrings/mise-archive-keyring.gpg \
    && chmod 0644 /etc/apt/keyrings/mise-archive-keyring.gpg \
    && echo "deb [signed-by=/etc/apt/keyrings/mise-archive-keyring.gpg arch=${TARGETARCH:-amd64}] https://mise.jdx.dev/deb stable main" \
        >/etc/apt/sources.list.d/mise.list \
    && apt-get update \
    && apt-get install -y --no-install-recommends mise

RUN systemctl disable docker.service docker.socket containerd.service >/dev/null 2>&1 || true \
    && rm -f \
        /etc/systemd/system/multi-user.target.wants/docker.service \
        /etc/systemd/system/multi-user.target.wants/containerd.service \
        /etc/systemd/system/sockets.target.wants/docker.socket

RUN sed -i 's/^# *\(de_DE.UTF-8 UTF-8\)/\1/' /etc/locale.gen \
    && sed -i 's/^# *\(en_US.UTF-8 UTF-8\)/\1/' /etc/locale.gen \
    && locale-gen \
    && update-locale LANG=C.UTF-8

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

RUN --mount=type=cache,target=/var/cache/apt,sharing=locked \
    --mount=type=cache,target=/var/lib/apt/lists,sharing=locked \
    install -d -m 0755 /usr/share/keyrings \
    && curl -fsSL https://packages.cloud.google.com/apt/doc/apt-key.gpg \
        | gpg --dearmor -o /usr/share/keyrings/cloud.google.gpg \
    && echo "deb [signed-by=/usr/share/keyrings/cloud.google.gpg] https://packages.cloud.google.com/apt cloud-sdk main" \
        >/etc/apt/sources.list.d/google-cloud-sdk.list \
    && apt-get update \
    && apt-get install -y --no-install-recommends google-cloud-cli

RUN --mount=type=cache,target=/var/cache/apt,sharing=locked \
    --mount=type=cache,target=/var/lib/apt/lists,sharing=locked \
    install -d -m 0755 /usr/share/keyrings \
    && curl -fsSL https://pkgs.tailscale.com/stable/debian/trixie.noarmor.gpg \
        -o /usr/share/keyrings/tailscale-archive-keyring.gpg \
    && curl -fsSL https://pkgs.tailscale.com/stable/debian/trixie.tailscale-keyring.list \
        -o /etc/apt/sources.list.d/tailscale.list \
    && apt-get update \
    && apt-get install -y --no-install-recommends tailscale

# One public IP per host NATs all containers; the host tailscaled owns 41641.
# Ephemeral port => distinct NAT mapping per container, no collision. Self-allocating, scales to N.
RUN printf 'PORT="0"\nFLAGS=""\n' > /etc/default/tailscaled

RUN systemctl disable tailscaled.service >/dev/null 2>&1 || true \
    && systemctl mask tailscaled.service >/dev/null 2>&1 || true \
    && rm -f /etc/systemd/system/multi-user.target.wants/tailscaled.service

RUN printf 'net.ipv4.ip_forward = 1\nnet.ipv6.conf.all.forwarding = 1\n' >/etc/sysctl.d/99-sandcastle-tailscale.conf

# Samba serves the tenant user's home (/home/<user>) and /workspace over SMB.
# smbd autostarts on every machine; the shares stay inaccessible until a Samba
# account exists (created by `sandcastle password set`). nmbd (NetBIOS name
# broadcast) is not needed, so it is masked.
RUN systemctl enable smbd.service >/dev/null 2>&1 || true \
    && systemctl disable nmbd.service >/dev/null 2>&1 || true \
    && systemctl mask nmbd.service >/dev/null 2>&1 || true \
    && rm -f /etc/systemd/system/multi-user.target.wants/nmbd.service

RUN install -d \
        /etc/sandcastle/caddy \
        /run/tailscale \
        /var/lib/sandcastle \
        /var/lib/tailscale \
        /workspace \
        /run/sshd

# Cheap, frequently-changing layers go last so the heavy package layers above
# stay cached across commits. The version stamp changes every commit, so it is
# the final layer before WORKDIR/CMD.
COPY sandcastle-bootstrap /usr/local/bin/sandcastle-bootstrap
RUN chmod 0755 /usr/local/bin/sandcastle-bootstrap

# Global git defaults and aliases; per-user identity is written at bootstrap
# from the tenant owner (see sandcastle-bootstrap).
COPY gitconfig /etc/gitconfig
RUN chmod 0644 /etc/gitconfig

# Samba global config. The actual shares are hostname-prefixed and rendered at
# machine-configure time by sandcastle-bootstrap into the included file below;
# an empty placeholder ships in the image so smbd's `include` resolves on first
# boot (before bootstrap runs). Per-user Samba accounts are created at runtime
# by `sandcastle password set`.
COPY smb.conf /etc/samba/smb.conf
RUN chmod 0644 /etc/samba/smb.conf \
    && : > /etc/samba/sandcastle-shares.conf \
    && chmod 0644 /etc/samba/sandcastle-shares.conf

ARG SANDCASTLE_IMAGE_TEMPLATE=base
ARG SANDCASTLE_IMAGE_TAG=sandcastle/base:latest
ARG SANDCASTLE_IMAGE_VERSION=unknown
ARG SANDCASTLE_IMAGE_COMMIT_DATE=unknown
RUN printf 'Sandcastle %s image\nVersion: %s\nImage: %s\nLast commit: %s\n\n' \
        "$SANDCASTLE_IMAGE_TEMPLATE" \
        "$SANDCASTLE_IMAGE_VERSION" \
        "$SANDCASTLE_IMAGE_TAG" \
        "$SANDCASTLE_IMAGE_COMMIT_DATE" \
        >/etc/issue \
    && cp /etc/issue /etc/issue.net

WORKDIR /workspace
CMD ["sleep", "infinity"]
