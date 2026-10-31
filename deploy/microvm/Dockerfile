# x86_64 keeps Ubuntu and Google Chrome. Google publishes no Linux arm64 Chrome,
# and Ubuntu's chromium is a snap stub, so build-images.sh builds aarch64 on
# Debian trixie and installs Debian's chromium package.
ARG COMPUTER_BASE=mirror.gcr.io/library/ubuntu:24.04
FROM mirror.gcr.io/library/node:22-bookworm-slim AS node-runtime
FROM mirror.gcr.io/library/golang:1.26.8-bookworm@sha256:a688600ca24f8a4d3ca77f95b0dd40704a9fc787c826660eb7ba0b641b8b175d AS gogcli-build
RUN CGO_ENABLED=0 GOBIN=/out go install github.com/openclaw/gogcli/cmd/gog@v0.40.0
FROM ${COMPUTER_BASE} AS computer-base
ARG DEBIAN_FRONTEND=noninteractive
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates curl wget gnupg bash coreutils util-linux iproute2 procps \
    nano less ripgrep tar \
    xvfb fluxbox xdotool imagemagick x11-utils dbus-x11 ffmpeg \
    python3 python3-venv python3-websocket git openssh-client unzip fonts-liberation fonts-noto-cjk \
    && if [ "$(dpkg --print-architecture)" = amd64 ]; then \
         curl -fsSL https://dl.google.com/linux/linux_signing_key.pub | gpg --dearmor -o /usr/share/keyrings/google-chrome.gpg \
         && echo 'deb [arch=amd64 signed-by=/usr/share/keyrings/google-chrome.gpg] https://dl.google.com/linux/chrome/deb/ stable main' > /etc/apt/sources.list.d/google-chrome.list \
         && apt-get update && apt-get install -y --no-install-recommends google-chrome-stable \
         && echo google-chrome-stable > /etc/tofi-browser-command; \
       else \
         apt-get install -y --no-install-recommends chromium \
         && echo chromium > /etc/tofi-browser-command; \
       fi \
    && rm -rf /var/lib/apt/lists/* \
    && (getent passwd 1000 >/dev/null || useradd -m -u 1000 -s /bin/bash bot) \
    && mkdir -p /workspace /run /tmp /dev/shm /var/tmp /var/log \
    && "$(cat /etc/tofi-browser-command)" --version > /etc/tofi-browser-version \
    && dpkg-query -W > /etc/tofi-image-packages
FROM computer-base
COPY --from=node-runtime /usr/local/ /usr/local/
COPY --from=gogcli-build /out/gog /usr/local/bin/gog
# Microsoft's Playwright MCP server, pinned by package-lock.json (decision
# 2026-10-29). The guest starts it on the first browser_ tool call and attaches
# it to the shared Chrome over DevTools (browser_engine=playwright). No browser
# is downloaded: --ignore-scripts, and the image's own Chrome/Chromium is used.
COPY playwright-mcp/package.json playwright-mcp/package-lock.json /usr/local/lib/tofi-playwright/
RUN cd /usr/local/lib/tofi-playwright && npm ci --omit=dev --ignore-scripts --no-audit --no-fund \
    && npm cache clean --force && chmod -R a+rX /usr/local/lib/tofi-playwright
RUN apt-get update && apt-get install -y --no-install-recommends busybox dbus pcmanfm feh tint2 xterm xclip adwaita-icon-theme \
    && rm -rf /var/lib/apt/lists/* && dpkg-query -W > /etc/tofi-image-packages
# open-computer-use (iFurySt, MIT), pinned by release and checksum (decision
# 2026-10-30). One 2 MB Go binary with an embedded Python AT-SPI bridge; the
# guest starts it on the first desktop_ tool call (desktop_engine=a11y). It
# needs python3-gi, the Atspi typelib and the AT-SPI registry (apt, below).
ARG OCU_VERSION=1.1.2
ARG OCU_SHA256_AMD64=2a6ff9a189388dc007a646ce9f7195cea110a16aab3d0e99d0e6766581e4b3d7
ARG OCU_SHA256_ARM64=c0a5af62ac9eb5b1ac2a7a8901c7d2e0952f795173d10057a3fa19e780e6744a
RUN apt-get update && apt-get install -y --no-install-recommends python3-gi gir1.2-atspi-2.0 at-spi2-core \
    && rm -rf /var/lib/apt/lists/* \
    && arch="$(dpkg --print-architecture)" \
    && if [ "$arch" = amd64 ]; then sum="$OCU_SHA256_AMD64"; else sum="$OCU_SHA256_ARM64"; fi \
    && curl -fsSL -o /tmp/ocu.tar.gz "https://github.com/iFurySt/open-computer-use/releases/download/v${OCU_VERSION}/open-computer-use-cli-${OCU_VERSION}-linux-${arch}.tar.gz" \
    && echo "${sum}  /tmp/ocu.tar.gz" | sha256sum -c - \
    && tar -xzf /tmp/ocu.tar.gz -C /usr/local/bin open-computer-use \
    && chmod 755 /usr/local/bin/open-computer-use && rm -f /tmp/ocu.tar.gz \
    && /usr/local/bin/open-computer-use --version \
    && dpkg-query -W > /etc/tofi-image-packages
# English-only office, PDF, OCR and charting tooling from the distro (works
# offline, runs as uid 1000). python-pptx has no noble package: it is a pure
# Python wheel installed without dependencies (lxml, XlsxWriter, Pillow and
# typing-extensions come from apt) through a throwaway venv, so pip itself stays
# out of the image. Other languages are the Bot's to fetch into user
# directories: tessdata through TESSDATA_PREFIX (set by the guest), fonts under
# ~/.local/share/fonts (listed in local.conf below).
RUN apt-get update && apt-get install -y --no-install-recommends \
    libreoffice-core libreoffice-writer libreoffice-calc libreoffice-impress \
    tesseract-ocr poppler-utils pandoc fontconfig \
    python3-openpyxl python3-docx python3-matplotlib python3-pandas python3-reportlab python3-pypdf python3-pil \
    python3-lxml python3-xlsxwriter python3-typing-extensions \
    && rm -rf /var/lib/apt/lists/* \
    && python3 -m venv /tmp/pptx-venv \
    && /tmp/pptx-venv/bin/pip install --no-cache-dir --no-deps --target "$(python3 -c 'import sysconfig; print(sysconfig.get_path("purelib"))')" python-pptx==1.0.2 \
    && rm -rf /tmp/pptx-venv \
    && printf '<?xml version="1.0"?>\n<!DOCTYPE fontconfig SYSTEM "fonts.dtd">\n<fontconfig>\n  <dir>~/.local/share/fonts</dir>\n</fontconfig>\n' > /etc/fonts/local.conf \
    && dpkg-query -W > /etc/tofi-image-packages
COPY tofi-guest /usr/local/bin/tofi-guest
COPY init.sh /sbin/tofi-init
COPY assets/tofi-wallpaper.png /usr/share/backgrounds/tofi.png
RUN chmod 755 /sbin/tofi-init /usr/local/bin/tofi-guest
ENTRYPOINT ["/sbin/tofi-init"]
