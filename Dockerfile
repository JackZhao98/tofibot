# Build the shared browser UI first, then compile the Go control plane.
FROM node:22-alpine AS ui-build
WORKDIR /src/ui
COPY ui/package.json ui/package-lock.json ./
RUN npm ci
COPY ui/ ./
RUN npm run build

FROM golang:1.25.11-alpine AS go-build
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
FROM alpine:3.22
LABEL io.tofi.account-runtime="1"
RUN apk add --no-cache ca-certificates \
  && addgroup -S -g 10001 tofi && adduser -S -u 10001 -G tofi -h /app tofi \
  && mkdir -p /app/data /app/ui \
  && chown -R tofi:tofi /app
WORKDIR /app
COPY --from=go-build /out/tofi /app/tofi
COPY --from=ui-build /src/ui/dist /app/ui
RUN chown -R tofi:tofi /app
USER tofi

ENV TOFI_LISTEN=0.0.0.0:8321 \
    TOFI_DATA_DIR=/app/data \
    TOFI_UI_DIR=/app/ui
EXPOSE 8321
VOLUME ["/app/data"]
ENTRYPOINT ["/app/tofi"]
