#!/bin/sh
# PostToolUse hook: regenerate brain/index.md (gitignored) from the note files.
# Matcher is "brain/", so this only fires on brain edits.
cat > /dev/null
script="${CLAUDE_PROJECT_DIR}/scripts/brain-index.sh"
[ -f "$script" ] || exit 0
exec sh "$script" "${CLAUDE_PROJECT_DIR}/brain"
