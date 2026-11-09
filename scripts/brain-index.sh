#!/bin/sh
# brain-index.sh — generate brain/index.md from the note files on disk.
# brain/index.md is gitignored: nobody edits it, so parallel orders that add
# notes never conflict on it. Usage: brain-index.sh [brain-dir]  (default: brain)

set -eu

BRAIN_DIR="${1:-brain}"
[ -d "$BRAIN_DIR" ] || exit 0
INDEX="$BRAIN_DIR/index.md"

# All .md files except index.md, as relative paths without the extension.
# plans/ and archive/plans/ subdirectories have their own index.
disk=$(find "$BRAIN_DIR" -name '*.md' ! -name index.md -type f |
  sed "s|^$BRAIN_DIR/||; s|\.md\$||" |
  grep -v '^plans/.\+/' | grep -v '^archive/plans/.\+/' | LC_ALL=C sort || true)

section() { # title, grep-pattern
  files=$(printf '%s\n' "$disk" | grep -E "$2" || true)
  [ -n "$files" ] || return 0
  printf '\n## %s\n' "$1"
  printf '%s\n' "$files" | sed 's/.*/- [[&]]/'
}

tmp=$(mktemp "$BRAIN_DIR/.index.XXXXXX")
trap 'rm -f "$tmp"' EXIT INT TERM
{
  echo '# Brain'
  section Architecture '^(architecture|vision)$'
  section Principles '^principles$'
  section Codebase '^codebase/'
  section Notes '^notes/'
  section Audits '^audits/'
  section Backlog '^todos$'
  [ -f "$BRAIN_DIR/plans/index.md" ] && printf '\n## Plans\n- [[plans/index]]\n'
  [ -f "$BRAIN_DIR/archive/plans/index.md" ] && printf '\n## Archived Plans\n- [[archive/plans/index]]\n'
  true
} > "$tmp"

if ! cmp -s "$tmp" "$INDEX" 2>/dev/null; then mv "$tmp" "$INDEX"; fi
