FROM mirror.gcr.io/library/node:22-bookworm-slim AS node-runtime
FROM mirror.gcr.io/library/golang:1.26.8-bookworm@sha256:a688600ca24f8a4d3ca77f95b0dd40704a9fc787c826660eb7ba0b641b8b175d AS gogcli-build
RUN CGO_ENABLED=0 GOBIN=/out go install github.com/openclaw/gogcli/cmd/gog@v0.40.0
FROM mirror.gcr.io/library/ubuntu:24.04 AS computer-base
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
