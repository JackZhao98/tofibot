FROM python:3.12-slim
# Keep util-linux on the mount(2) sequence covered by the confined Worker policy.
ENV PYTHONDONTWRITEBYTECODE=1 PYTHONUNBUFFERED=1 LIBMOUNT_FORCE_MOUNT2=always
WORKDIR /opt/tofi-worker
COPY deploy/microvm/account_capacity.py deploy/microvm/account_provisioner.py \
     deploy/microvm/account_adoption.py \
     deploy/microvm/worker_cgroups.py deploy/microvm/worker_supervisor.py \
     deploy/microvm/worker_entrypoint.py deploy/microvm/manager.py \
     deploy/microvm/account_release_check.py ./
RUN apt-get update && apt-get install -y --no-install-recommends \
      iproute2 iptables util-linux procps e2fsprogs ca-certificates \
    && rm -rf /var/lib/apt/lists/*
ENTRYPOINT ["python3", "/opt/tofi-worker/worker_entrypoint.py", "--config", "/etc/tofi-worker/config.json"]
