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

RUN install -d \
        /etc/sandcastle/caddy \
        /var/lib/sandcastle \
        /workspace \
        /run/sshd

COPY sandcastle-bootstrap /usr/local/bin/sandcastle-bootstrap
RUN chmod 0755 /usr/local/bin/sandcastle-bootstrap

WORKDIR /workspace
CMD ["/bin/bash", "-l"]
