#!/bin/bash
# Prevent git commits to main branch in this repo.

input=$(cat)
command=$(echo "$input" | jq -r '.tool_input.command // empty')

# Only care about git commit commands
if ! echo "$command" | grep -qE '\bgit\b.*\bcommit\b'; then
  exit 0
fi

# Resolve this repo's root (hook lives at .claude/hooks/ inside the repo)
repo_dir="$(cd "$(dirname "$0")/../.." && pwd)"

# Check if the commit targets this repo by checking its current branch
branch=$(git -C "$repo_dir" branch --show-current 2>/dev/null)
if [ "$branch" = "main" ] || [ "$branch" = "master" ]; then
  echo "BLOCKED: Cannot commit to '$branch' in $repo_dir. Create a feature branch first." >&2
  exit 2
fi

exit 0
