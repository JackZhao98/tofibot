# E2B cloud computer: the deploy/microvm computer image plus a start script
# that serves tofi-guest on TCP with a bearer token (docs/design/environments.md
# E3/E4). Build the base first, then:
#   docker build --build-arg COMPUTER_IMAGE=<base image> -f deploy/e2b/Dockerfile deploy/e2b
ARG COMPUTER_IMAGE
FROM ${COMPUTER_IMAGE}
LABEL org.opencontainers.image.source="https://github.com/JackZhao98/tofibot" \
      org.opencontainers.image.description="TOFI Bot computer for E2B (tofi-guest over TCP)"
# curl is installed by the base image (E2B's ready check uses it).
RUN command -v curl >/dev/null \
    && mkdir -p /var/lib/tofi /workspace \
    && chown 1000:1000 /var/lib/tofi /workspace \
    && chmod 0755 /var/lib/tofi
COPY --chmod=0755 start.sh /usr/local/bin/tofi-e2b-start
ENTRYPOINT ["/usr/local/bin/tofi-e2b-start"]
CMD []
