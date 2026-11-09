#!/bin/sh
# PostToolUse hook: regenerate brain/index.md (gitignored) from the note files.
cat > /dev/null
exec sh "${CLAUDE_PROJECT_DIR}/scripts/brain-index.sh" "${CLAUDE_PROJECT_DIR}/brain"
