#!/usr/bin/env bash
# Run locally what .github/workflows/ci.yml runs, job by job, with the same environment.
# Every failed workflow run emails the owner: run this before pushing to main or tagging (docs/process.md 3.6).
#
#   scripts/ci-local.sh            same as --changed
#   scripts/ci-local.sh --changed  fast static checks always; Go tests, UI suites and the rest only for what changed
#   scripts/ci-local.sh --all      every ci.yml job (go, ui, ui-guards, ui-catalog, python, playwright-mcp, shell, runner-root, compose);
#                                  steps that need Linux or docker print a clear "not run here" line (CI runs them)
#   --base <ref>                   what "changed" is measured against (default: merge-base with origin/main, else main)
#   --only <job>[,<job>]           run just these jobs      --skip <job>[,<job>]   leave these out
#   --list                         print the job names and exit
#
# Environment: PLAYWRIGHT_MODULE (default: tofibot-web's playwright; CI installs playwright@1.58.2 in a temp dir).
# The first failing job stops the run (exit 1); a summary of all jobs is printed either way.
set -uo pipefail

repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo" || exit 2

mode=changed base="" only="" skip=""
while [ $# -gt 0 ]; do
  case "$1" in
    --all) mode=all ;;
    --changed) mode=changed ;;
    --base) base="${2:?--base needs a ref}"; shift ;;
    --only) only="${2:?--only needs job names}"; shift ;;
    --skip) skip="${2:?--skip needs job names}"; shift ;;
    --list) mode=list ;;
    -h|--help) sed -n '2,15p' "$0"; exit 0 ;;
    *) echo "ci-local: unknown argument $1" >&2; exit 2 ;;
  esac
  shift
done

# ---- the job table: name, what it is, how ci.yml runs it
JOBS="go-gofmt go-build go-vet go-test ui-typecheck ui-i18n ui-motion-tokens ui-build ui-layers ui-dialogs ui-overlays ui-shell-budget ui-catalog python playwright-mcp shell runner-root compose"
if [ "$mode" = list ]; then echo "$JOBS" | tr ' ' '\n'; exit 0; fi

export PLAYWRIGHT_MODULE="${PLAYWRIGHT_MODULE:-/Users/jackzhao/Developer/sentiosurge/tofibot-web/node_modules/playwright/index.mjs}"
if [ ! -f "$PLAYWRIGHT_MODULE" ]; then
  for cand in /Users/jackzhao/.claude/skills/gstack/node_modules/playwright/index.mjs "$repo/ui/node_modules/playwright/index.mjs"; do
    [ -f "$cand" ] && { PLAYWRIGHT_MODULE="$cand"; export PLAYWRIGHT_MODULE; break; }
  done
fi
if [ ! -f "$PLAYWRIGHT_MODULE" ]; then
  echo "ci-local: no Playwright at $PLAYWRIGHT_MODULE; the browser jobs (ui-overlays, ui-shell-budget, ui-catalog, ...) will fail." >&2
  echo "          Set PLAYWRIGHT_MODULE=/path/to/playwright/index.mjs (for example ~/.claude/skills/gstack/node_modules/playwright/index.mjs)." >&2
fi
logdir="$(mktemp -d "${TMPDIR:-/tmp}/ci-local.XXXXXX")"

# ---- what changed (committed since base, staged, unstaged, untracked)
changed=""
if [ "$mode" = changed ]; then
  if [ -z "$base" ]; then
    for ref in origin/main main; do
      if git rev-parse -q --verify "$ref" >/dev/null; then base="$(git merge-base HEAD "$ref")"; break; fi
    done
  fi
  [ -n "$base" ] || { echo "ci-local: no base ref; pass --base <ref>" >&2; exit 2; }
  changed="$( { git diff --name-only "$base" HEAD; git diff --name-only HEAD; git ls-files --others --exclude-standard; } | sort -u | sed '/^$/d')"
  echo "ci-local: --changed against $(git rev-parse --short "$base"): $(printf '%s\n' "$changed" | sed '/^$/d' | wc -l | tr -d ' ') file(s)"
fi
touched() { # touched <egrep pattern>: did any changed file match? (always true in --all)
  [ "$mode" = all ] && return 0
  printf '%s\n' "$changed" | grep -Eq "$1"
}

in_list() { case ",$2," in *",$1,"*) return 0 ;; esac; return 1; }
wanted() { # wanted <job> <egrep pattern for its inputs, or "always">
  [ -z "$only" ] || in_list "$1" "$only" || return 1
  ! in_list "$1" "$skip" || return 1
  [ "$2" = always ] && return 0
  [ -n "$only" ] && return 0
  touched "$2"
}

summary=()
failed=""
run() { # run <job> <command...>
  local job="$1"; shift
  local log="$logdir/$job.log" start=$SECONDS rc
  echo "=== $job"
  "$@" 2>&1 | tee "$log"
  rc=${PIPESTATUS[0]}
  if [ "$rc" -eq 0 ]; then summary+=("PASS  $job ($((SECONDS - start))s)"); else summary+=("FAIL  $job (exit $rc, $((SECONDS - start))s, log $log)"); failed="$job"; fi
  return "$rc"
}
skipped() { summary+=("skip  $1 (nothing it covers changed)"); }
job() { # job <name> <input pattern|always> <command...>
  local name="$1" pat="$2"; shift 2
  if wanted "$name" "$pat"; then
    [ -n "$failed" ] && return
    run "$name" "$@" || true
  else
    [ -n "$only" ] && ! in_list "$name" "$only" && return
    in_list "$name" "$skip" && { summary+=("skip  $name (--skip)"); return; }
    skipped "$name"
  fi
}

# ---- Go packages to test in --changed mode: the package directory of every changed file under Go source trees
go_packages() {
  printf '%s\n' "$changed" | grep -E '^(internal|cmd|pkg)/' | while read -r f; do
    d="$(dirname "$f")"
    while [ "$d" != . ] && ! ls "$d"/*.go >/dev/null 2>&1; do d="$(dirname "$d")"; done
    [ -d "$d" ] && [ "$d" != . ] && echo "./$d"
  done | sort -u
}
go_test() {
  export TOFI_SKIP_TIMING=1
  if [ "$mode" = all ] || printf '%s\n' "$changed" | grep -Eq '^(go\.mod|go\.sum)$'; then
    go test -timeout 30m ./...
  else
    local pkgs; pkgs="$(go_packages)"
    [ -n "$pkgs" ] || { echo "no changed Go package"; return 0; }
    # shellcheck disable=SC2086
    go test -timeout 30m $pkgs
  fi
}

npm_ui() { npm --prefix ui run "$@"; }
guards() { TOFI_SKIP_TIMING=1 SHELL_BUDGET_ONLY=struct npm_ui "$@"; }
need_playwright() { [ -f "$PLAYWRIGHT_MODULE" ] || { echo "PLAYWRIGHT_MODULE not found: $PLAYWRIGHT_MODULE" >&2; return 1; }; }

GO='\.go$|^go\.(mod|sum)$|^internal/|^cmd/'
UI='^ui/'
UISRC='^ui/(src|scripts|public|index\.html|package(-lock)?\.json|vite[^/]*\.ts|tsconfig[^/]*\.json)'
PY='^(deploy/microvm|deploy/self-host|scripts/release|scripts/browser-bench|internal/extensions/builtin_skills)/|^install\.sh$'
SH='^(install\.sh|deploy/self-host/bin/|scripts/release/make_bundle\.sh|scripts/check-gofmt\.sh|scripts/check-release-notes\.sh|release-notes/)'
CO='^(compose\.yaml|deploy/self-host/)'

# Fast static checks run in --changed mode whatever changed ("always").
job go-gofmt     always scripts/check-gofmt.sh
job go-build     always go build ./...
job go-vet       always go vet ./...
job go-test      "$GO"  go_test
job ui-typecheck "$UI"  npm_ui typecheck
job ui-i18n      always npm_ui test:i18n
job ui-motion-tokens always npm_ui test:motion-tokens
job ui-build     "$UI"  npm_ui build
if wanted ui-layers always || wanted ui-dialogs always || wanted ui-overlays always || wanted ui-shell-budget always || wanted ui-catalog "$UISRC"; then
  need_playwright || { summary+=("FAIL  playwright (PLAYWRIGHT_MODULE missing)"); failed="playwright"; }
fi
job ui-layers    always guards test:layers
job ui-dialogs   always guards test:dialogs
job ui-overlays  always guards test:overlays
job ui-shell-budget always guards test:shell-budget
job ui-catalog   "$UISRC" npm_ui test:catalog
job python       "$PY"  bash -c "python3 -m unittest discover -s deploy/microvm -p 'test_*.py' && python3 -m unittest discover -s deploy/self-host -p 'test_*.py' && python3 -m unittest discover -s scripts/release -p 'test_*.py' && python3 -m unittest discover -s scripts/browser-bench -p 'test_*.py' && python3 -m unittest discover -s internal/extensions/builtin_skills/tests -p 'test_*.py'"
# Mirrors ci.yml playwright-mcp. The pinned package is installed once into deploy/microvm/playwright-mcp/node_modules
# (about 19 MB, no browser download) and reused. The guest relay tests are linux-only files: elsewhere the guest package
# is cross-vetted for linux and the tests are left to CI.
playwright_mcp() {
  local dir=deploy/microvm/playwright-mcp
  if [ ! -f "$dir/node_modules/@playwright/mcp/cli.js" ]; then
    echo "installing the pinned @playwright/mcp (no browser download)"
    (cd "$dir" && npm ci --omit=dev --ignore-scripts --no-audit --no-fund) || return 1
  fi
  (cd "$dir" && node check-tools.mjs) || return 1
  if [ "$(uname -s)" = Linux ]; then
    go test ./internal/computer/guest -run 'Playwright' -count=1
  else
    echo "ci-local: guest relay tests (playwright_linux_test.go) need Linux and were NOT run here; CI runs them. Cross-vetting for linux instead."
    GOOS=linux go vet ./internal/computer/guest
  fi
}
runner_root() {
  if ! docker info >/dev/null 2>&1; then
    echo "ci-local: docker is not available; the runner-root tests (root on Linux) were NOT run here; CI runs them."
    return 0
  fi
  docker run --rm -v "$PWD:/src" -w /src golang:1.25.11 go test ./internal/mcprunner -count=1 -v -run 'Root|RealUID'
}
job playwright-mcp '^(deploy/microvm/playwright-mcp/|internal/computer/guest/playwright)' playwright_mcp
job shell        "$SH"  bash -c "bash -n install.sh deploy/self-host/bin/tofi scripts/release/make_bundle.sh scripts/check-gofmt.sh scripts/check-release-notes.sh && shellcheck install.sh deploy/self-host/bin/tofi scripts/release/make_bundle.sh scripts/check-gofmt.sh scripts/check-release-notes.sh && scripts/check-release-notes.sh --all"
job runner-root  '^internal/mcprunner/' runner_root
job compose      "$CO"  bash -c "
  docker compose -f deploy/self-host/compose.yaml --env-file deploy/self-host/testdata/tofi.env --profile tls config --quiet &&
  docker compose -f deploy/self-host/compose.yaml --env-file deploy/self-host/testdata/tofi-direct.env config --quiet &&
  docker compose -f deploy/self-host/compose.yaml --env-file deploy/self-host/testdata/tofi.env --profile computer-kvm config --services | sort | tr '\n' ' ' | grep -qx 'app worker ' &&
  docker compose -f deploy/self-host/compose.yaml --env-file deploy/self-host/testdata/tofi.env config --services | sort | tr '\n' ' ' | grep -qx 'app ' &&
  docker compose -f deploy/self-host/compose.yaml --env-file deploy/self-host/testdata/tofi-none.env config --services | sort | tr '\n' ' ' | grep -qx 'app ' &&
  docker compose -f compose.yaml config --quiet"

# Changed UI browser suites that ci.yml does not run (flaky on shared runners, so local only): run the ones whose script changed.
if [ "$mode" = changed ] && [ -z "$failed" ] && [ -z "$only" ]; then
  for f in $(printf '%s\n' "$changed" | grep -E '^ui/scripts/test-[^/]+\.mjs$'); do
    name="test:$(basename "$f" .mjs | sed 's/^test-//')"
    case "$name" in test:layers|test:dialogs|test:overlays|test:shell-budget|test:catalog) continue ;; esac
    grep -q "\"$name\"" ui/package.json && { run "ui-$name" npm_ui "$name" || break; }
  done
fi

echo
echo "ci-local summary ($mode):"
printf '  %s\n' "${summary[@]}"
if [ -n "$failed" ]; then
  echo
  echo "ci-local: FAILED at $failed. Do not push."
  [ -f "$logdir/$failed.log" ] && { echo "--- last 40 lines of $logdir/$failed.log"; tail -40 "$logdir/$failed.log"; }
  exit 1
fi
echo "ci-local: all selected jobs passed (logs in $logdir)"
