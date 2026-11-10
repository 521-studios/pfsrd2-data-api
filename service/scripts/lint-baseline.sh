#!/usr/bin/env bash
# Runs staticcheck, deadcode and gocognit -over 10 and compares each against
# lint-baseline/<tool>.txt. CI fails on any finding that isn't in the baseline,
# and on any baseline entry that's now fixed, so the list can only get shorter.
# Line numbers are stripped so unrelated edits don't churn the baseline.
# gocognit entries carry the score, so a baselined function that gets more
# complex fails too (and one that gets simpler needs --update).
#
#   scripts/lint-baseline.sh           check (CI)
#   scripts/lint-baseline.sh --update  rewrite the baselines from current output
set -uo pipefail
cd "$(dirname "$0")/.."

mode=${1:-check}
status=0
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

# A tool that can't run, or a build that fails, prints nothing, which would
# otherwise read as "every finding fixed" (and --update would wipe the baselines).
for tool in staticcheck deadcode gocognit; do
	command -v "$tool" >/dev/null || { echo "$tool not on PATH" >&2; exit 2; }
done
go build ./... || { echo "go build failed; not linting" >&2; exit 2; }

check() {
	local name=$1 base=lint-baseline/$1.txt
	shift
	# The tools exit non-zero when they report findings; the diff is what decides.
	"$@" | sed -E 's/:[0-9]+:[0-9]+:/:/' | LC_ALL=C sort -u >"$tmp/$name"
	if [[ $mode == --update ]]; then
		cp "$tmp/$name" "$base"
		return
	fi
	if [[ ! -f $base ]]; then
		printf '%s: %s is missing (run scripts/lint-baseline.sh --update)\n' "$name" "$base"
		status=1
		return
	fi
	local new fixed
	new=$(LC_ALL=C comm -13 "$base" "$tmp/$name")
	fixed=$(LC_ALL=C comm -23 "$base" "$tmp/$name")
	if [[ -n $new ]]; then
		printf '%s: new findings (fix them; the baseline is for pre-existing code only):\n%s\n\n' "$name" "$new"
		status=1
	fi
	if [[ -n $fixed ]]; then
		printf '%s: fixed, so remove from %s (or run scripts/lint-baseline.sh --update):\n%s\n\n' "$name" "$base" "$fixed"
		status=1
	fi
}

check staticcheck staticcheck ./...
check deadcode deadcode ./...
check gocognit gocognit -over 10 -ignore '_test\.go$' -f '{{.Complexity}} {{.PkgName}} {{.FuncName}} {{.Pos.Filename}}' .

exit $status
