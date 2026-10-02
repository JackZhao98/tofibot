FROM golang:1.25.11-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/tofi-mcp-runner ./cmd/tofi-mcp-runner

# gogcli's v0.40.0 container tag currently has only a linux/amd64 manifest.
# Build its pinned Go module for the target architecture so Docker users on
# arm64 can run the same example plugin.
FROM golang:1.26.8-bookworm@sha256:a688600ca24f8a4d3ca77f95b0dd40704a9fc787c826660eb7ba0b641b8b175d AS gogcli-build
RUN CGO_ENABLED=0 GOBIN=/out go install github.com/openclaw/gogcli/cmd/gog@v0.40.0

FROM node:22-bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates python3 python3-venv python3-pip \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd -g 10001 runner && useradd -u 10001 -g 10001 -M -s /usr/sbin/nologin runner \
    && mkdir -p /runner/state /runner/auth \
    && chown -R runner:runner /runner
COPY --from=build /out/tofi-mcp-runner /usr/local/bin/tofi-mcp-runner
COPY --from=gogcli-build /out/gog /usr/local/bin/gog
USER runner
ENV TOFI_MCP_RUNNER_STATE=/runner/state TOFI_MCP_RUNNER_TOKEN_FILE=/runner/auth/token TOFI_MCP_RUNNER_LISTEN=0.0.0.0:8390
EXPOSE 8390
ENTRYPOINT ["/usr/local/bin/tofi-mcp-runner"]
