FROM node:22-bookworm-slim AS node-runtime
FROM golang:1.26.8-bookworm@sha256:a688600ca24f8a4d3ca77f95b0dd40704a9fc787c826660eb7ba0b641b8b175d AS gogcli-build
RUN CGO_ENABLED=0 GOBIN=/out go install github.com/openclaw/gogcli/cmd/gog@v0.40.0
FROM ubuntu:24.04 AS computer-base
ARG DEBIAN_FRONTEND=noninteractive
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates curl wget gnupg bash coreutils util-linux iproute2 procps \
    nano less ripgrep tar \
    xvfb fluxbox xdotool imagemagick x11-utils dbus-x11 ffmpeg \
    python3 python3-venv python3-websocket git openssh-client unzip fonts-liberation fonts-noto-cjk \
    && curl -fsSL https://dl.google.com/linux/linux_signing_key.pub | gpg --dearmor -o /usr/share/keyrings/google-chrome.gpg \
    && echo 'deb [arch=amd64 signed-by=/usr/share/keyrings/google-chrome.gpg] https://dl.google.com/linux/chrome/deb/ stable main' > /etc/apt/sources.list.d/google-chrome.list \
    && apt-get update && apt-get install -y --no-install-recommends google-chrome-stable \
    && rm -rf /var/lib/apt/lists/* \
    && (getent passwd 1000 >/dev/null || useradd -m -u 1000 -s /bin/bash bot) \
    && mkdir -p /workspace /run /tmp /dev/shm /var/tmp /var/log \
    && google-chrome-stable --version > /etc/tofi-browser-version \
    && dpkg-query -W > /etc/tofi-image-packages
FROM computer-base
COPY --from=node-runtime /usr/local/ /usr/local/
COPY --from=gogcli-build /out/gog /usr/local/bin/gog
RUN apt-get update && apt-get install -y --no-install-recommends busybox dbus pcmanfm feh tint2 xterm xclip adwaita-icon-theme \
    && rm -rf /var/lib/apt/lists/* && dpkg-query -W > /etc/tofi-image-packages
COPY tofi-guest /usr/local/bin/tofi-guest
COPY init.sh /sbin/tofi-init
COPY assets/tofi-wallpaper.png /usr/share/backgrounds/tofi.png
RUN chmod 755 /sbin/tofi-init /usr/local/bin/tofi-guest
ENTRYPOINT ["/sbin/tofi-init"]
