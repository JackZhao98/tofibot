#!/usr/bin/env bash
# Run every UI browser suite once, one after another, and print a results table.
#
# GitHub CI runs only a few UI suites (ci.yml: i18n, motion-tokens, layers, dialogs, overlays, shell-budget, catalog), and
# ci-local runs the other ones only when their own script changed. The rest fall behind later UI changes without anyone
# noticing (2026-11-04: 16 of 93 failed, two of them real bugs). Run this before every rc tag (docs/process.md 3.6).
#
#   scripts/ui-suites-all.sh                 every test:* script in ui/package.json
#   scripts/ui-suites-all.sh test:chat-motion test:preloader    just these
#
# Environment: PLAYWRIGHT_MODULE — a Playwright whose browsers are installed, Chromium and WebKit (several suites run both;
# CI uses 1.58.2). Default: gstack's Playwright 1.58.2, as in ci-local. Results and one log per suite go to $UI_SUITES_OUT
# (default: a new temp dir). Run it on a quiet machine: the frame-timing checks fail under heavy load.
# Exit 0 only when every suite exits 0 and none prints SKIP.
set -uo pipefail

repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo/ui" || exit 2
export PLAYWRIGHT_MODULE="${PLAYWRIGHT_MODULE:-$HOME/.claude/skills/gstack/node_modules/playwright/index.mjs}"
export TOFI_SKIP_TIMING=1 SHELL_BUDGET_ONLY=struct
[ -f "$PLAYWRIGHT_MODULE" ] || { echo "ui-suites-all: PLAYWRIGHT_MODULE not found: $PLAYWRIGHT_MODULE" >&2; exit 2; }
out="${UI_SUITES_OUT:-$(mktemp -d -t ui-suites)}"
mkdir -p "$out"

if [ $# -gt 0 ]; then suites="$*"
else suites="$(node -e 'console.log(Object.keys(require("./package.json").scripts).filter(k => k.startsWith("test:")).join(" "))')"; fi

echo "ui-suites-all: building ui/dist (logs in $out)"
npm run -s build > "$out/_build.log" 2>&1 || { echo "ui-suites-all: build failed, see $out/_build.log" >&2; exit 1; }

# test:conversation-route-browser needs its Go fixture running: start one, point the suite at it, stop it afterwards.
route_suite() {
  local manifest="$out/route-manifest.json" log="$1" pid code origin
  rm -f "$manifest"
  (cd "$repo" && TOFI_ROUTE_BROWSER_MANIFEST="$manifest" go test ./internal/app -run '^TestConversationRouteBrowserFixture$' -count=1 -timeout 20m > "$out/route-fixture.log" 2>&1) &
  pid=$!
  for _ in $(seq 1 180); do [ -s "$manifest" ] && break; sleep 1; done
  if [ ! -s "$manifest" ]; then echo "route fixture did not start, see $out/route-fixture.log" > "$log"; kill "$pid" 2>/dev/null; return 1; fi
  TOFI_ROUTE_BROWSER_MANIFEST="$manifest" TOFI_ROUTE_BROWSER_OUTPUT="$out/route-browser" npm run -s test:conversation-route-browser > "$log" 2>&1
  code=$?
  origin="$(node -e 'console.log(JSON.parse(require("fs").readFileSync(process.argv[1], "utf8")).origin)' "$manifest" 2>/dev/null)"
  [ -n "$origin" ] && curl -s -X POST "$origin/acceptance/stop" > /dev/null 2>&1
  wait "$pid" 2>/dev/null
  return "$code"
}

printf "suite\texit\tseconds\tskip\n" > "$out/results.tsv"
failed=0
for suite in $suites; do
  log="$out/${suite#test:}.log"
  start=$(date +%s)
  if [ "$suite" = test:conversation-route-browser ]; then route_suite "$log"; else npm run -s "$suite" > "$log" 2>&1; fi
  code=$?
  skip=$(grep -cE '^SKIP|SKIP:' "$log")
  printf "%s\t%s\t%s\t%s\n" "$suite" "$code" "$(( $(date +%s) - start ))" "$skip" >> "$out/results.tsv"
  if [ "$code" -ne 0 ] || [ "$skip" -gt 0 ]; then failed=$((failed + 1)); echo "  FAIL  $suite (exit $code, skip lines $skip) — $log"; else echo "  ok    $suite"; fi
done

total=$(($(wc -l < "$out/results.tsv") - 1))
echo "ui-suites-all: $((total - failed)) of $total passed; table: $out/results.tsv"
[ "$failed" -eq 0 ]
