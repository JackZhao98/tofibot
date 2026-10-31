# Third-party notices

This file lists third-party software that TOFI ships or builds into its images, with the license each upstream project declares. It is a factual list. It is not legal advice. Each component stays under its own license, not the TOFI license in `LICENSE`.

If a notice is missing, open an issue.

## 1. Go modules in the TOFI binaries

These modules are compiled into the programs under `cmd/`. The list comes from `go list -deps ./cmd/...`. The license name comes from the license file in each module. Versions are those pinned in `go.mod` and `go.sum`.

| Component | Version | License | Source URL |
|---|---|---|---|
| `github.com/creack/pty` | v1.1.24 | MIT | https://pkg.go.dev/github.com/creack/pty@v1.1.24 |
| `github.com/dustin/go-humanize` | v1.0.1 | MIT | https://pkg.go.dev/github.com/dustin/go-humanize@v1.0.1 |
| `github.com/google/jsonschema-go` | v0.4.3 | MIT | https://pkg.go.dev/github.com/google/jsonschema-go@v0.4.3 |
| `github.com/google/uuid` | v1.6.0 | BSD-3-Clause | https://pkg.go.dev/github.com/google/uuid@v1.6.0 |
| `github.com/mattn/go-isatty` | v0.0.20 | MIT | https://pkg.go.dev/github.com/mattn/go-isatty@v0.0.20 |
| `github.com/modelcontextprotocol/go-sdk` | v1.8.0 | Apache-2.0 | https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk@v1.8.0 |
| `github.com/ncruces/go-strftime` | v0.1.9 | MIT | https://pkg.go.dev/github.com/ncruces/go-strftime@v0.1.9 |
| `github.com/remyoudompheng/bigfft` | v0.0.0-20230129092748-24d4a6f8daec | BSD-3-Clause | https://pkg.go.dev/github.com/remyoudompheng/bigfft@v0.0.0-20230129092748-24d4a6f8daec |
| `github.com/segmentio/asm` | v1.1.3 | MIT | https://pkg.go.dev/github.com/segmentio/asm@v1.1.3 |
| `github.com/segmentio/encoding` | v0.5.4 | MIT | https://pkg.go.dev/github.com/segmentio/encoding@v0.5.4 |
| `github.com/tidwall/gjson` | v1.18.0 | MIT | https://pkg.go.dev/github.com/tidwall/gjson@v1.18.0 |
| `github.com/tidwall/match` | v1.1.1 | MIT | https://pkg.go.dev/github.com/tidwall/match@v1.1.1 |
| `github.com/tidwall/pretty` | v1.2.0 | MIT | https://pkg.go.dev/github.com/tidwall/pretty@v1.2.0 |
| `github.com/yosida95/uritemplate/v3` | v3.0.2 | BSD-3-Clause | https://pkg.go.dev/github.com/yosida95/uritemplate/v3@v3.0.2 |
| `golang.org/x/crypto` | v0.52.0 | BSD-3-Clause | https://pkg.go.dev/golang.org/x/crypto@v0.52.0 |
| `golang.org/x/exp` | v0.0.0-20250620022241-b7579e27df2b | BSD-3-Clause | https://pkg.go.dev/golang.org/x/exp@v0.0.0-20250620022241-b7579e27df2b |
| `golang.org/x/image` | v0.44.0 | BSD-3-Clause | https://pkg.go.dev/golang.org/x/image@v0.44.0 |
| `golang.org/x/oauth2` | v0.35.0 | BSD-3-Clause | https://pkg.go.dev/golang.org/x/oauth2@v0.35.0 |
| `golang.org/x/sync` | v0.20.0 | BSD-3-Clause | https://pkg.go.dev/golang.org/x/sync@v0.20.0 |
| `golang.org/x/sys` | v0.45.0 | BSD-3-Clause | https://pkg.go.dev/golang.org/x/sys@v0.45.0 |
| `golang.org/x/time` | v0.15.0 | BSD-3-Clause | https://pkg.go.dev/golang.org/x/time@v0.15.0 |
| `gopkg.in/natefinch/lumberjack.v2` | v2.2.1 | MIT | https://pkg.go.dev/gopkg.in/natefinch/lumberjack.v2@v2.2.1 |
| `gopkg.in/yaml.v3` | v3.0.1 | MIT and Apache-2.0 (two license files) | https://pkg.go.dev/gopkg.in/yaml.v3@v3.0.1 |
| `modernc.org/libc` | v1.66.10 | BSD-3-Clause | https://pkg.go.dev/modernc.org/libc@v1.66.10 |
| `modernc.org/mathutil` | v1.7.1 | BSD-3-Clause | https://pkg.go.dev/modernc.org/mathutil@v1.7.1 |
| `modernc.org/memory` | v1.11.0 | BSD-3-Clause | https://pkg.go.dev/modernc.org/memory@v1.11.0 |
| `modernc.org/sqlite` | v1.42.2 | BSD-3-Clause | https://pkg.go.dev/modernc.org/sqlite@v1.42.2 |
| `mvdan.cc/sh/v3` | v3.13.0 | BSD-3-Clause | https://pkg.go.dev/mvdan.cc/sh/v3@v3.13.0 |

The program `gogcli` is not a Go dependency of the TOFI binaries. The images in sections 4 and 5 build it separately.

## 2. Web app runtime dependencies

These are the `dependencies` of `ui/package.json`. Versions and license names come from `ui/package-lock.json`. Their own dependencies are pinned in the same lock file.

| Component | Version | License | Source URL |
|---|---|---|---|
| `@emoji-mart/data` | 1.2.1 | MIT | https://www.npmjs.com/package/@emoji-mart/data |
| `@radix-ui/react-alert-dialog` | 1.2.1 | MIT | https://www.npmjs.com/package/@radix-ui/react-alert-dialog |
| `@radix-ui/react-dialog` | 1.3.1 | MIT | https://www.npmjs.com/package/@radix-ui/react-dialog |
| `@radix-ui/react-dropdown-menu` | 2.2.1 | MIT | https://www.npmjs.com/package/@radix-ui/react-dropdown-menu |
| `@radix-ui/react-popover` | 1.3.1 | MIT | https://www.npmjs.com/package/@radix-ui/react-popover |
| `@radix-ui/react-select` | 2.4.1 | MIT | https://www.npmjs.com/package/@radix-ui/react-select |
| `@xterm/addon-fit` | 0.11.0 | MIT | https://www.npmjs.com/package/@xterm/addon-fit |
| `@xterm/xterm` | 6.0.0 | MIT | https://www.npmjs.com/package/@xterm/xterm |
| `emoji-mart` | 5.6.0 | MIT | https://www.npmjs.com/package/emoji-mart |
| `i18next` | 26.4.2 | MIT | https://www.npmjs.com/package/i18next |
| `react` | 19.3.0 | MIT | https://www.npmjs.com/package/react |
| `react-dom` | 19.3.0 | MIT | https://www.npmjs.com/package/react-dom |
| `react-i18next` | 17.0.16 | MIT | https://www.npmjs.com/package/react-i18next |
| `react-markdown` | 10.1.0 | MIT | https://www.npmjs.com/package/react-markdown |
| `remark-cjk-friendly` | 2.3.1 | MIT | https://www.npmjs.com/package/remark-cjk-friendly |
| `remark-gfm` | 4.0.1 | MIT | https://www.npmjs.com/package/remark-gfm |

The Web app also keeps its own notices:

- [`ui/THIRD_PARTY_NOTICES.md`](ui/THIRD_PARTY_NOTICES.md) lists the fonts and the bundled Radix, Floating UI and related packages.
- [`ui/public/licenses`](ui/public/licenses) holds the full license texts that the Web app serves.

## 3. Fonts and brand assets

### Fonts

| Component | Version | License | Source URL |
|---|---|---|---|
| Fredoka (Latin subset of the variable font, modified) | see `ui/THIRD_PARTY_NOTICES.md` | SIL OFL 1.1 | https://github.com/google/fonts/tree/main/ofl/fredoka |
| JetBrains Mono (Regular and SemiBold, unmodified) | 2.304 | SIL OFL 1.1 | https://github.com/JetBrains/JetBrainsMono |

Font files are in `ui/src/assets/fonts`. The full OFL texts are in [`ui/public/licenses/fredoka-OFL.txt`](ui/public/licenses/fredoka-OFL.txt), [`ui/public/licenses/jetbrains-mono-OFL.txt`](ui/public/licenses/jetbrains-mono-OFL.txt) and [`assets/OFL-Fredoka.txt`](assets/OFL-Fredoka.txt). The Web app ships no CJK font.

### Brand and icon assets

Provider marks are in `assets/brand-icons`. Copies for the Web app are in `ui/public/brand/services`. Each mark names a service. Trademarks stay with their owners. Use of a mark does not imply endorsement. Details for each file (source, hash, license, changes) are in [`assets/brand-icons/NOTICE.md`](assets/brand-icons/NOTICE.md) and [`assets/brand-icons/manifest.json`](assets/brand-icons/manifest.json).

| Component | Version | License | Source URL |
|---|---|---|---|
| Simple Icons marks (Notion, Linear, Robinhood, Brave, SearXNG) | commit c71c69e3a27e4436936b831c72643e6513b9f49b | CC0-1.0 for the package; brand rights stay with the owners | https://github.com/simple-icons/simple-icons |
| Google Workspace product icons and Google "G" | n/a | Google trademarks, not open-licensed; used unmodified | https://about.google/brand-resource-center/ |
| GitHub mark | n/a | GitHub brand kit terms | https://brand.github.com/ |
| Discord mark | n/a | Discord brand terms | https://discord.com/branding |
| Slack mark | n/a | Slack trademark | https://slack.com/media-kit |
| Microsoft Learn symbol | n/a | Microsoft trademark | https://learn.microsoft.com/ |
| Context7 mark | n/a | Project repository is MIT; trademark with Upstash | https://github.com/upstash/context7 |
| Yahoo Finance icon | n/a | Yahoo trademark | https://finance.yahoo.com/ |
| Tavily, Firecrawl and Bright Data site icons | n/a | Vendor trademarks, as published by each vendor | https://tavily.com/ , https://firecrawl.dev/ , https://brightdata.com/ |

The TOFI glyph library, the Bot avatars and the banner are original TOFI artwork.

## 4. Bot computer Guest image

The build files are `deploy/microvm/Dockerfile`, `deploy/microvm/build-images.sh` and `deploy/microvm/playwright-mcp/package-lock.json`. The table lists the major components. License names are those that each upstream project declares.

| Component | Version | License | Source URL |
|---|---|---|---|
| Ubuntu (x86_64 base) or Debian trixie (aarch64 base) | 24.04 / trixie | Each package keeps its own license | https://ubuntu.com/ , https://www.debian.org/ |
| Google Chrome (x86_64 only) | stable channel | Google Chrome Terms of Service; includes open source parts | https://www.google.com/chrome/ |
| Chromium (aarch64 only, Debian package) | Debian package version | BSD-3-Clause and the licenses of included parts | https://www.chromium.org/ |
| Node.js runtime (copied from the `node:22-bookworm-slim` image) | 22 | MIT and the licenses of bundled parts | https://github.com/nodejs/node |
| `@playwright/mcp` (Playwright MCP server) | 0.0.83 | Apache-2.0 | https://github.com/microsoft/playwright-mcp |
| `playwright` and `playwright-core` | 1.64.0-alpha-1790635538000 | Apache-2.0 | https://github.com/microsoft/playwright |
| open-computer-use (iFurySt) | 1.1.2 | MIT | https://github.com/iFurySt/open-computer-use |
| gogcli (`gog`) | v0.40.0 | see upstream | https://github.com/openclaw/gogcli |
| python-pptx | 1.0.2 | MIT | https://github.com/scanny/python-pptx |
| Python packages from the distribution: openpyxl, python-docx, matplotlib, pandas, reportlab, pypdf, Pillow, lxml, XlsxWriter, typing-extensions, python3-gi | distribution versions | Each package declares its own license | https://packages.ubuntu.com/ |
| LibreOffice (core, Writer, Calc, Impress) | distribution version | MPL-2.0 | https://www.libreoffice.org/ |
| Tesseract OCR | distribution version | Apache-2.0 | https://github.com/tesseract-ocr/tesseract |
| Poppler utilities | distribution version | GPL-2.0-or-later (upstream also offers GPL-3.0) | https://poppler.freedesktop.org/ |
| Pandoc | distribution version | GPL-2.0-or-later | https://pandoc.org/ |
| FFmpeg, ImageMagick, Xvfb, Fluxbox, xdotool, ripgrep, Git, OpenSSH client and the other distribution tools in the Dockerfile | distribution versions | Each package declares its own license | https://packages.ubuntu.com/ |
| Fonts: Liberation, Noto CJK | distribution versions | SIL OFL 1.1 | https://github.com/liberationfonts/liberation-fonts , https://github.com/notofonts/noto-cjk |

The Guest image also contains the `tofi-guest` program, which is built from this repository (section 1 lists its Go modules).

The distribution packages in the image keep their own license files under `/usr/share/doc/<package>` in the image. The file `/etc/tofi-image-packages` in the image lists every installed package and its version.

## 5. Container base images

These are the `FROM` lines in the Dockerfiles of this repository. Each base image keeps its own license files. Each package in a base image keeps its own license.

| Component | Version | License | Source URL |
|---|---|---|---|
| `mirror.gcr.io/library/node` (UI build stage, `Dockerfile`) | 22-alpine | Node.js: MIT; Alpine packages: each its own | https://hub.docker.com/_/node |
| `mirror.gcr.io/library/golang` (Go build stage, `Dockerfile`) | 1.25.11-alpine | BSD-3-Clause (Go); Alpine packages: each its own | https://hub.docker.com/_/golang |
| `scratch` (guest artifact stage, `Dockerfile`) | n/a | Empty image | https://hub.docker.com/_/scratch |
| `mirror.gcr.io/library/alpine` (TOFI runtime image, `Dockerfile`) | 3.22 | Each package declares its own license | https://hub.docker.com/_/alpine |
| `mirror.gcr.io/library/golang` (`deploy/mcp-runner/Dockerfile`, tool runner build stage) | 1.25.11-bookworm | BSD-3-Clause (Go); Debian packages: each its own | https://hub.docker.com/_/golang |
| `mirror.gcr.io/library/golang` (gogcli build stage, `deploy/microvm/Dockerfile` and `deploy/mcp-runner/Dockerfile`) | 1.26.8-bookworm (pinned by digest) | BSD-3-Clause (Go); Debian packages: each its own | https://hub.docker.com/_/golang |
| `mirror.gcr.io/library/node` (`deploy/mcp-runner/Dockerfile`, tool runner image; `deploy/microvm/Dockerfile`, Node.js runtime stage) | 22-bookworm-slim | Node.js: MIT; Debian packages: each its own | https://hub.docker.com/_/node |
| `mirror.gcr.io/library/ubuntu` (Guest image base, `deploy/microvm/Dockerfile`) | 24.04 | Each package declares its own license | https://hub.docker.com/_/ubuntu |
| `mirror.gcr.io/library/debian` (aarch64 Guest base, set as `COMPUTER_BASE` by `build-images.sh`) | trixie-slim | Each package declares its own license | https://hub.docker.com/_/debian |
| `mirror.gcr.io/library/python` (Worker image, `deploy/microvm/Worker.Dockerfile`) | 3.12-slim | PSF-2.0 (Python); Debian packages: each its own | https://hub.docker.com/_/python |

The tool runner image also installs Debian packages (`python3`, `python3-venv`, `python3-pip`, `iproute2`, `iptables`, `util-linux`, `ca-certificates`) and `gog` (gogcli v0.40.0, license: see upstream).
