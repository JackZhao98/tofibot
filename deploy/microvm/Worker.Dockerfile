# Build from the repository root: docker build -f deploy/microvm/Worker.Dockerfile .
FROM python:3.12-slim
ARG TOFI_SOURCE_COMMIT=""
LABEL io.tofi.account-worker="1" \
      io.tofi.account-guest-protocol="tofi-account-guest-v1" \
      org.opencontainers.image.revision=$TOFI_SOURCE_COMMIT
# Keep util-linux on the mount(2) sequence covered by the confined Worker policy.
ENV PYTHONDONTWRITEBYTECODE=1 PYTHONUNBUFFERED=1 LIBMOUNT_FORCE_MOUNT2=always
WORKDIR /opt/tofi-worker
RUN apt-get update && apt-get install -y --no-install-recommends \
      iproute2 iptables util-linux procps e2fsprogs ca-certificates \
    && rm -rf /var/lib/apt/lists/*
COPY deploy/microvm/account_capacity.py deploy/microvm/account_provisioner.py \
     deploy/microvm/account_adoption.py \
     deploy/microvm/worker_cgroups.py deploy/microvm/worker_supervisor.py \
     deploy/microvm/worker_entrypoint.py deploy/microvm/manager.py \
     deploy/microvm/account_release_check.py ./
ENTRYPOINT ["python3", "/opt/tofi-worker/worker_entrypoint.py", "--config", "/etc/tofi-worker/config.json"]
