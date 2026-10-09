# Build the shared browser UI first, then compile the Go control plane.
FROM mirror.gcr.io/library/node:22-alpine AS ui-build
WORKDIR /src/ui
COPY ui/package.json ui/package-lock.json ./
RUN npm ci
COPY ui/ ./
RUN npm run build

FROM mirror.gcr.io/library/golang:1.25.11-alpine AS go-build
WORKDIR /src
ENV CGO_ENABLED=0 GOOS=linux
COPY go.mod go.sum ./
RUN go mod download
COPY . ./
RUN go build -trimpath -ldflags="-s -w" -o /out/tofi ./cmd/tofi \
  && go build -trimpath -ldflags="-s -w" -o /out/tofi-guest ./cmd/tofi-guest

FROM scratch AS guest-artifact
COPY --from=go-build /out/tofi-guest /tofi-guest

# The runtime image contains one non-root process and a named persistent data volume.
FROM mirror.gcr.io/library/alpine:3.22
RUN apk add --no-cache ca-certificates \
  && addgroup -S -g 10001 tofi && adduser -S -u 10001 -G tofi -h /app tofi \
  && mkdir -p /app/data /app/ui \
  && chown -R tofi:tofi /app
WORKDIR /app
# --chown at copy time: a later `RUN chown -R` would duplicate /app in a new layer.
COPY --from=go-build --chown=tofi:tofi /out/tofi /app/tofi
COPY --from=ui-build --chown=tofi:tofi /src/ui/dist /app/ui
USER tofi

ENV TOFI_LISTEN=0.0.0.0:8321 \
    TOFI_DATA_DIR=/app/data \
    TOFI_UI_DIR=/app/ui
EXPOSE 8321
VOLUME ["/app/data"]
# Labels last: the per-commit ARG would otherwise invalidate the package layer cache.
ARG TOFI_SOURCE_COMMIT=""
LABEL io.tofi.account-runtime="1" \
      io.tofi.data-schema="tofi-account-data-v1" \
      io.tofi.account-guest-protocol="tofi-account-guest-v1" \
      org.opencontainers.image.revision=$TOFI_SOURCE_COMMIT
ENTRYPOINT ["/app/tofi"]
