#!/usr/bin/env bash
# Every release tag has its release notes in release-notes/<tag>.md (English, short, for people who use the product).
# The release workflow runs this for the tag; ci-local.sh runs it for every file.
#
#   scripts/check-release-notes.sh v0.6.0-rc.1   one tag: the file must exist and be well formed
#   scripts/check-release-notes.sh --all         every file in release-notes/
#
# A file starts with a front matter block (version, date, channel), then a "# " title and at least one bullet.
# version equals the file name, date is YYYY-MM-DD, channel is rc for a -rc.N tag and stable otherwise.
# The manual embeds a copy under internal/manual/generated/whats-new/ (go generate ./internal/app); TestManualWhatsNewGenerated keeps it current.
set -uo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.." || exit 2

fail=0
err() { echo "release-notes: $*" >&2; fail=1; }

check_file() {
  local tag="$1" file="release-notes/$1.md"
  if [[ ! $tag =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-rc\.[0-9]+)?$ ]]; then err "$tag is not a release tag (vMAJOR.MINOR.PATCH or vMAJOR.MINOR.PATCH-rc.N)"; return; fi
  if [[ ! -f $file ]]; then err "$file is missing. Write the release notes for $tag before you tag it."; return; fi
  local head version date channel want
  head=$(sed -n '1p' "$file")
  [[ $head == '---' ]] || { err "$file must start with a front matter block (---)"; return; }
  version=$(sed -n '2,6p' "$file" | sed -n 's/^version: *//p' | head -1)
  date=$(sed -n '2,6p' "$file" | sed -n 's/^date: *//p' | head -1)
  channel=$(sed -n '2,6p' "$file" | sed -n 's/^channel: *//p' | head -1)
  [[ $version == "$tag" ]] || err "$file: version is '$version', expected '$tag'"
  [[ $date =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}$ ]] || err "$file: date must be YYYY-MM-DD, got '$date'"
  want=stable; [[ $tag == *-rc.* ]] && want=rc
  [[ $channel == "$want" ]] || err "$file: channel is '$channel', expected '$want'"
  sed -n '2,12p' "$file" | grep -q '^---$' || err "$file: the front matter block is not closed"
  grep -q '^# .' "$file" || err "$file: a '# ' title line is required"
  grep -q '^- .' "$file" || err "$file: at least one '- ' bullet is required"
  if grep -Eq '(sk-[A-Za-z0-9]{16,}|-----BEGIN [A-Z ]*PRIVATE KEY)' "$file"; then err "$file: looks like it holds a secret"; fi
}

case "${1:-}" in
  --all)
    shopt -s nullglob
    for f in release-notes/*.md; do check_file "$(basename "$f" .md)"; done
    ;;
  "") echo "usage: $0 <tag> | --all" >&2; exit 2 ;;
  *) check_file "$1" ;;
esac
exit $fail
