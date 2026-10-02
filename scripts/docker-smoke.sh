#!/usr/bin/env bash
set -euo pipefail

IMAGE=${1:-tofi:ci}
SMOKE_OWNER_AUTH=${TOFI_SMOKE_OWNER_AUTH:-1}
[[ "$SMOKE_OWNER_AUTH" == 0 || "$SMOKE_OWNER_AUTH" == 1 ]] || { echo 'TOFI_SMOKE_OWNER_AUTH must be 0 or 1' >&2; exit 2; }
# This is a disposable, loopback-published acceptance container, never the
# production data volume. LAN HTTP is enabled only to exercise owner setup
# from the host through Docker's bridge without transmitting real credentials.
CONTAINER=tofi-alpha-smoke-$(date +%s)-$$
VOLUME=tofi-alpha-smoke-data-$(date +%s)-$$
HOST_PORT=$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1", 0)); print(s.getsockname()[1]); s.close()')
BASE_URL="http://127.0.0.1:${HOST_PORT}"

cleanup() {
  docker rm -f "$CONTAINER" >/dev/null 2>&1 || true
  docker volume rm "$VOLUME" >/dev/null 2>&1 || true
}
trap cleanup EXIT INT TERM

docker volume create "$VOLUME" >/dev/null
docker run -d \
  --name "$CONTAINER" \
  --memory 2g \
  --cpus 1 \
  --env TOFI_ENVIRONMENT=acceptance \
  --env TOFI_LISTEN=0.0.0.0:8321 \
  --env TOFI_DATA_DIR=/app/data \
  --env TOFI_UI_DIR=/app/ui \
  --env "TOFI_OWNER_AUTH=${SMOKE_OWNER_AUTH}" \
  --env TOFI_OWNER_ALLOW_LAN_HTTP=1 \
  --publish "127.0.0.1:${HOST_PORT}:8321" \
  --volume "${VOLUME}:/app/data" \
  "$IMAGE" >/dev/null

TOFI_SMOKE_BASE_URL="$BASE_URL" TOFI_SMOKE_CONTAINER="$CONTAINER" TOFI_SMOKE_OWNER_AUTH="$SMOKE_OWNER_AUTH" python3 <<'PY'
import http.cookiejar
import json
import os
import re
import subprocess
import time
import urllib.error
import urllib.request
import uuid

base = os.environ["TOFI_SMOKE_BASE_URL"]
container = os.environ["TOFI_SMOKE_CONTAINER"]
cookies = http.cookiejar.CookieJar()
client = urllib.request.build_opener(urllib.request.ProxyHandler({}), urllib.request.HTTPCookieProcessor(cookies))
anonymous_client = urllib.request.build_opener(urllib.request.ProxyHandler({}))
owner_auth = os.environ["TOFI_SMOKE_OWNER_AUTH"] == "1"

def call(method, path, body=None, expected=(200,), origin=None, anonymous=False):
    data = None if body is None else json.dumps(body).encode()
    request = urllib.request.Request(base + path, data=data, method=method)
    request.add_header("Origin", base if origin is None else origin)
    if data is not None:
        request.add_header("Content-Type", "application/json")
    try:
        with (anonymous_client if anonymous else client).open(request, timeout=5) as response:
            status = response.status
            payload = response.read()
            content_type = response.headers.get("Content-Type", "")
    except urllib.error.HTTPError as error:
        status = error.code
        payload = error.read()
        content_type = error.headers.get("Content-Type", "")
    if status not in expected:
        raise AssertionError(f"{method} {path}: status {status}, expected {expected}: {payload[:300]!r}")
    return status, payload, content_type

for _ in range(60):
    try:
        status, _, content_type = call("GET", "/health")
        if status == 200 and content_type.startswith("application/json"):
            break
    except (OSError, urllib.error.URLError):
        pass
    time.sleep(0.5)
else:
    raise AssertionError("container did not become healthy")

_, health, _ = call("GET", "/health")
health = json.loads(health)
assert health["environment"] == "acceptance"
_, discovery, _ = call("GET", "/api/server-info")
discovery = json.loads(discovery)
assert discovery["service"] == "tofi" and discovery["protocol_version"] == 1
assert discovery["instance_id"] == health["instance_id"]
assert discovery["auth"]["mode"] == ("owner-password" if owner_auth else "none")
assert discovery["tenancy"] == {"mode": "single"}

_, html, html_type = call("GET", "/")
assert html_type.startswith("text/html"), html_type
html_text = html.decode()
scripts = re.findall(r'<script[^>]+src=["\']([^"\']+\.js)["\']', html_text)
assert scripts, "UI index has no JavaScript bundle"
_, js, js_type = call("GET", scripts[0])
assert js_type.startswith(("application/javascript", "text/javascript")), js_type
assert len(js) > 100, "JavaScript bundle is empty"

if owner_auth:
    _, initial_raw, _ = call("GET", "/api/auth/session")
    initial = json.loads(initial_raw)
    assert initial["enabled"] and initial["setup_required"] and not initial["authenticated"]
    call("GET", "/api/bots", expected=(401,), anonymous=True)
    secret = subprocess.check_output(["docker", "exec", container, "cat", "/app/data/owner-bootstrap.secret"], text=True).strip()
    # This password and one-time secret exist only inside the disposable smoke volume.
    setup = {"bootstrap_secret": secret, "username": "smoke-owner", "email": "smoke@example.invalid", "password": "synthetic-smoke-password-2984"}
    _, setup_raw, _ = call("POST", "/api/auth/setup", setup)
    state = json.loads(setup_raw)
    assert state["authenticated"] and not state["setup_required"] and state["owner"]["role"] == "admin"
    assert state["owner"]["username"] == setup["username"]
    assert subprocess.run(["docker", "exec", container, "test", "!", "-e", "/app/data/owner-bootstrap.secret"], check=False).returncode == 0
    call("POST", "/api/auth/setup", setup, expected=(401,))
    _, anonymous_state_raw, _ = call("GET", "/api/auth/session", anonymous=True)
    assert "owner" not in json.loads(anonymous_state_raw)
    call("POST", "/api/auth/login", {"identifier": "smoke-owner", "password": "wrong-password"}, expected=(401,), anonymous=True)
    call("GET", "/api/bots", expected=(401,), anonymous=True)
else:
    assert discovery["auth"] == {"mode": "none"}

call("GET", "/api/bots")
call("GET", "/api/auth/codex")
call("POST", "/api/bots", {"name": "must-not-create"}, expected=(403,), origin="https://unrelated.example")

onboarding_request = {"onboarding": True, "client_creation_id": str(uuid.uuid4())}
_, onboarding_raw, _ = call("POST", "/api/bots", onboarding_request, expected=(201,))
onboarding = json.loads(onboarding_raw)
_, repeated_raw, _ = call("POST", "/api/bots", onboarding_request)
assert json.loads(repeated_raw)["id"] == onboarding["id"]
assert onboarding["name"] == "New Bot"
_, welcome_raw, _ = call("GET", f'/api/conversations/{onboarding["dm_conversation_id"]}/messages')
welcome = json.loads(welcome_raw)["messages"]
assert len(welcome) == 1 and welcome[0]["role"] == "assistant" and welcome[0]["sender_bot_id"] == onboarding["id"]

_, first_raw, _ = call("POST", "/api/bots", {"name": "smoke-a", "instructions": "test bot A"}, expected=(201,))
_, second_raw, _ = call("POST", "/api/bots", {"name": "smoke-b", "instructions": "test bot B"}, expected=(201,))
first = json.loads(first_raw)
second = json.loads(second_raw)
_, group_raw, _ = call("POST", "/api/groups", {"name": "smoke-group", "bot_ids": [first["id"], second["id"]]}, expected=(201,))
group = json.loads(group_raw)
_, memory_raw, _ = call("POST", f'/api/conversations/{first["dm_conversation_id"]}/memories', {"content": "smoke memory"}, expected=(201,))
memory = json.loads(memory_raw)

# No model environment is passed to the container. This must be an explicit
# configuration error rather than a fake answer.
call("POST", f'/api/conversations/{first["dm_conversation_id"]}/messages', {
    "content": "should fail without a model",
    "client_message_id": "smoke-no-model",
}, expected=(503,))

_, bots_before, _ = call("GET", "/api/bots")
assert {item["id"] for item in json.loads(bots_before)["bots"]} >= {first["id"], second["id"]}

subprocess.run(["docker", "restart", container], check=True, stdout=subprocess.DEVNULL)
for _ in range(60):
    try:
        call("GET", "/health")
        break
    except (OSError, urllib.error.URLError, AssertionError):
        time.sleep(0.5)
else:
    raise AssertionError("container did not recover after restart")
_, restarted_discovery_raw, _ = call("GET", "/api/server-info")
assert json.loads(restarted_discovery_raw)["instance_id"] == discovery["instance_id"]
if owner_auth:
    _, resumed_state_raw, _ = call("GET", "/api/auth/session")
    resumed_state = json.loads(resumed_state_raw)
    assert resumed_state["authenticated"] and resumed_state["owner"]["username"] == "smoke-owner"

_, bots_after, _ = call("GET", "/api/bots")
persisted_bots = json.loads(bots_after)["bots"]
bot_ids = {item["id"] for item in persisted_bots}
assert {first["id"], second["id"]} <= bot_ids
assert onboarding["id"] in bot_ids
_, resumed_welcome, _ = call("GET", f'/api/conversations/{onboarding["dm_conversation_id"]}/messages')
assert json.loads(resumed_welcome)["messages"] == welcome
persisted_by_id = {item["id"]: item for item in persisted_bots}
assert persisted_by_id[first["id"]]["dm_conversation_id"] == first["dm_conversation_id"]
assert persisted_by_id[second["id"]]["dm_conversation_id"] == second["dm_conversation_id"]
_, conversations, _ = call("GET", "/api/conversations")
conversation_list = json.loads(conversations)["conversations"]
persisted_group = next((item for item in conversation_list if item["id"] == group["id"]), None)
assert persisted_group is not None
assert set(persisted_group["bot_ids"]) >= {first["id"], second["id"]}
_, memories, _ = call("GET", f'/api/conversations/{first["dm_conversation_id"]}/memories')
assert any(item["id"] == memory["id"] and item["content"] == "smoke memory" for item in json.loads(memories)["memories"])
if owner_auth:
    call("POST", "/api/auth/logout", {})
    call("GET", "/api/bots", expected=(401,))
    _, login_raw, _ = call("POST", "/api/auth/login", {"identifier": "SMOKE-OWNER", "password": "synthetic-smoke-password-2984"})
    assert json.loads(login_raw)["authenticated"]
    call("GET", "/api/bots")
PY

echo "Docker smoke passed for ${IMAGE} on 127.0.0.1:${HOST_PORT}"
