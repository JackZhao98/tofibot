#!/usr/bin/env bash
# gofmt guard with a shrink-only baseline (docs/conventions.md).
#   - a tracked .go file that gofmt would change and is not in .gofmt-baseline fails;
#   - a .gofmt-baseline entry that is now formatted (or gone) fails: delete the line.
# When .gofmt-baseline does not exist the check is a plain `gofmt -l` must-be-empty.
# Never add a line to the baseline to get a new file through: run `gofmt -w` on it.
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"
baseline=.gofmt-baseline
unformatted=$(git ls-files '*.go' | xargs gofmt -l | LC_ALL=C sort)
known=""
if [ -f "$baseline" ]; then known=$(grep -v '^[[:space:]]*\(#\|$\)' "$baseline" | LC_ALL=C sort); fi
fresh=$(LC_ALL=C comm -23 <(printf '%s\n' "$unformatted" | sed '/^$/d') <(printf '%s\n' "$known" | sed '/^$/d'))
stale=$(LC_ALL=C comm -13 <(printf '%s\n' "$unformatted" | sed '/^$/d') <(printf '%s\n' "$known" | sed '/^$/d'))
status=0
if [ -n "$fresh" ]; then
  echo "gofmt: these files are not formatted (run: gofmt -w <file>):" >&2
  printf "%s\n" "$fresh" | sed "s/^/  /" >&2
  status=1
fi
if [ -n "$stale" ]; then
  echo "gofmt: these files are formatted now; remove them from $baseline (the baseline only shrinks):" >&2
  printf "%s\n" "$stale" | sed "s/^/  /" >&2
  status=1
fi
if [ "$status" -eq 0 ]; then
  echo "gofmt: ok ($(printf '%s\n' "$known" | sed '/^$/d' | wc -l | tr -d ' ') file(s) still in the baseline)"
fi
exit "$status"
