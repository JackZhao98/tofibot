#!/usr/bin/env bash
# Run one of N shards of `go test ./...` (CI splits the Go tests across parallel jobs).
#   scripts/go-test-shard.sh <index> <count> [extra go test flags...]
# internal/app holds most of the time, so its top-level tests are split by name (sorted, round robin).
# Every other package goes to one shard by its position in `go list` (round robin).
# `--check <count>` only lists the tests and fails when the listing is empty.
set -euo pipefail
index=${1:?shard index}; count=${2:?shard count}; shift 2
heavy=github.com/JackZhao98/tofibot/internal/app

packages=(); while IFS= read -r line; do packages+=("$line"); done < <(go list ./... | grep -vx "$heavy")
tests=(); while IFS= read -r line; do tests+=("$line"); done < <(go test -list '.*' "$heavy" | grep -E '^(Test|Example|Fuzz)' | sort)

if [ "$index" = "--check" ]; then
  # Every test lands in exactly one shard by construction; check the listing itself is not empty.
  [ "${#tests[@]}" -gt 0 ] || { echo "go-test-shard: no tests listed in $heavy" >&2; exit 1; }
  echo "go-test-shard: ${#tests[@]} tests in internal/app, ${#packages[@]} other packages, $count shards"
  exit 0
fi

mine=()
for i in "${!tests[@]}"; do
  if [ $((i % count)) -eq "$index" ]; then mine+=("${tests[$i]}"); fi
done
others=()
for i in "${!packages[@]}"; do
  if [ $((i % count)) -eq "$index" ]; then others+=("${packages[$i]}"); fi
done

status=0
if [ "${#mine[@]}" -gt 0 ]; then
  pattern="^($(IFS='|'; echo "${mine[*]}"))\$"
  echo "go-test-shard $index/$count: ${#mine[@]} of ${#tests[@]} internal/app tests"
  go test "$@" -run "$pattern" "$heavy" || status=$?
fi
if [ "${#others[@]}" -gt 0 ]; then
  echo "go-test-shard $index/$count: ${#others[@]} other packages"
  go test "$@" "${others[@]}" || status=$?
fi
exit "$status"
