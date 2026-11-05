#!/bin/bash
# Local check of the E2B image on an x86_64 Linux host with Docker.
# Usage: deploy/e2b/smoke.sh IMAGE
set -uo pipefail
image=${1:?usage: smoke.sh IMAGE}
name="tofi-e2b-smoke-$$-$RANDOM"
port=8331
base="http://127.0.0.1:$port"
bot=11111111-1111-4111-8111-111111111111
fail=0

cleanup() { docker rm -f "$name" >/dev/null 2>&1 || true; }
trap cleanup EXIT

pass() { echo "PASS $1"; }
bad() { echo "FAIL $1"; fail=1; }
check() { # name, condition exit status
  if [[ $2 -eq 0 ]]; then pass "$1"; else bad "$1"; fi
}
# http METHOD PATH [TOKEN] [BODY] -> sets code, body
http() {
  local args=(-sS -o /tmp/tofi-e2b-smoke.$$ -w '%{http_code}' -X "$1" "$base$2")
  [[ -n "${3:-}" ]] && args+=(-H "Authorization: Bearer $3")
  [[ -n "${4:-}" ]] && args+=(-H 'Content-Type: application/json' -d "$4")
  code=$(curl "${args[@]}" 2>&1) || code=000
  body=$(cat /tmp/tofi-e2b-smoke.$$ 2>/dev/null || true)
  rm -f /tmp/tofi-e2b-smoke.$$
}
action() { # ACTION ARGS_JSON
  http POST /v1/action "$token" "{\"bot_id\":\"$bot\",\"run_id\":\"smoke\",\"action\":\"$1\",\"args\":$2}"
}

# --privileged: E2B runs this image inside a Firecracker VM as root; locally the
# desktop (Xvfb, Chrome sandbox, D-Bus) needs the same freedom. Published on
# loopback only.
docker run -d --name "$name" --privileged -p "127.0.0.1:$port:8331" "$image" >/dev/null
check "container started" $?

ready=1
for _ in $(seq 1 60); do
  http GET /health
  if [[ $code == 200 ]]; then ready=0; break; fi
  sleep 1
done
check "GET /health open" $ready
[[ $ready -eq 0 ]] || { docker logs "$name" 2>&1 | tail -20; exit 1; }

http GET /v1/info
[[ $code == 503 && $body == *token_missing* ]]; check "/v1/info is 503 token_missing before the token exists" $?

token=$(head -c 24 /dev/urandom | od -An -tx1 | tr -d ' \n')
printf '%s' "$token" | docker exec -i "$name" bash -c 'cat > /var/lib/tofi/guest-token'
check "token written" $?

http GET /v1/info "wrong-$token"
[[ $code == 401 ]]; check "wrong token is 401" $?
http GET /v1/info
[[ $code == 401 ]]; check "no token is 401" $?
http GET /v1/info "$token"
[[ $code == 200 ]]; check "right token /v1/info is 200" $?

action shell.exec '{"command":"echo ok","timeout_sec":20}'
[[ $code == 200 && $body == *'"ok":true'* && $body == *'"stdout":"ok'* ]]; check "shell.exec echo ok" $?

action desktop.start '{}'
[[ $code == 200 && $body == *'"ok":true'* ]]; check "desktop.start" $?
sleep 3
action desktop.capture '{}'
[[ $code == 200 && $body == *'data:image/png;base64,'* ]]; check "desktop.capture returns image data" $?
action desktop.stop '{}'
[[ $code == 200 && $body == *'"ok":true'* ]]; check "desktop.stop" $?

if [[ $fail -ne 0 ]]; then echo "SMOKE FAILED"; docker logs "$name" 2>&1 | tail -30; exit 1; fi
echo "SMOKE PASSED"
