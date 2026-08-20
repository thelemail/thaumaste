#!/usr/bin/env bash
set -euo pipefail

COMPLEMENT_SHA=6d2fdc286c2b44faaddd1037205869b2242a4005
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
COMPLEMENT_DIR="${COMPLEMENT_DIR:-$ROOT/.complement}"
IMAGE="${COMPLEMENT_BASE_IMAGE:-thaumaste:complement}"
PACKAGES="./tests/csapi"
OUT="$ROOT/complement"

mode="${1:-allowlist}"

fetch_complement() {
	if [ ! -d "$COMPLEMENT_DIR/.git" ]; then
		git clone -q https://github.com/matrix-org/complement.git "$COMPLEMENT_DIR"
	fi
	if [ "$(git -C "$COMPLEMENT_DIR" rev-parse HEAD)" != "$COMPLEMENT_SHA" ]; then
		git -C "$COMPLEMENT_DIR" fetch -q origin
		git -C "$COMPLEMENT_DIR" checkout -q "$COMPLEMENT_SHA"
	fi
}

build_image() {
	docker build -q -f "$ROOT/complement/Dockerfile" -t "$IMAGE" "$ROOT" >/dev/null
}

allowlist_regex() {
	grep -vE '^[[:space:]]*(#|$)' "$OUT/allowlist.txt" | paste -sd'|' -
}

normalise() {
	jq -Rc 'fromjson? // empty' "$OUT/output.jsonl" \
		| jq -sc '[.[]
			| select((.Action == "pass" or .Action == "fail" or .Action == "skip") and .Test != null)
			| {Action: .Action, Test: .Test}]
			| unique | sort_by(.Test)[]' \
		> "$OUT/results.jsonl"
}

report() {
	local total pass fail skip pct
	total=$(jq -sr '[.[] | select(.Test | contains("/") | not)] | length' "$OUT/results.jsonl")
	pass=$(jq -sr '[.[] | select((.Test | contains("/") | not) and .Action == "pass")] | length' "$OUT/results.jsonl")
	fail=$(jq -sr '[.[] | select((.Test | contains("/") | not) and .Action == "fail")] | length' "$OUT/results.jsonl")
	skip=$(jq -sr '[.[] | select((.Test | contains("/") | not) and .Action == "skip")] | length' "$OUT/results.jsonl")
	pct=$(awk -v p="$pass" -v t="$total" 'BEGIN{ if (t==0) print "0.0"; else printf "%.1f", (p*100)/t }')

	{
		echo "# Complement coverage"
		echo
		echo "Suite: \`$PACKAGES\` at complement \`${COMPLEMENT_SHA:0:7}\`."
		echo "Federation tests are not run: this server does not implement federation."
		echo
		echo "| tests | pass | fail | skip | passing |"
		echo "|------:|-----:|-----:|-----:|--------:|"
		echo "| $total | $pass | $fail | $skip | ${pct}% |"
		echo
		echo "## Passing"
		echo
		jq -sr '.[] | select((.Test | contains("/") | not) and .Action == "pass") | "- \(.Test)"' "$OUT/results.jsonl"
		echo
		echo "## Skipped"
		echo
		jq -sr '.[] | select((.Test | contains("/") | not) and .Action == "skip") | "- \(.Test)"' "$OUT/results.jsonl"
		echo
		echo "## Why the failures stop where they do"
		echo
		echo "Most frequent assertion failures across the run:"
		echo
		jq -Rc 'fromjson? // empty' "$OUT/output.jsonl" \
			| jq -r 'select(.Action == "output" and .Test != null) | .Output' \
			| grep -oE '(Expected|want) [0-9]{3}[^,]*, got [0-9]{3}|Expected [0-9]{3} [A-Za-z ]+, got [0-9]{3}' \
			| sort | uniq -c | sort -rn | head -5 \
			| sed 's/^ *\([0-9]*\) /- \1 x /'
		echo
		echo "## Failing"
		echo
		jq -sr '.[] | select((.Test | contains("/") | not) and .Action == "fail") | "- \(.Test)"' "$OUT/results.jsonl"
	} > "$OUT/COVERAGE.md"
}

case "$mode" in
allowlist)
	fetch_complement
	build_image
	cd "$COMPLEMENT_DIR"
	COMPLEMENT_BASE_IMAGE="$IMAGE" \
		go test -v -count=1 -timeout 10m -run "^($(allowlist_regex))$" $PACKAGES
	;;
full)
	fetch_complement
	build_image
	cd "$COMPLEMENT_DIR"
	COMPLEMENT_BASE_IMAGE="$IMAGE" COMPLEMENT_ENABLE_DIRTY_RUNS=1 \
		go test -json -count=1 -timeout 45m $PACKAGES > "$OUT/output.jsonl" || true
	normalise
	report
	jq -sr '"\([.[] | select(.Action == "pass")] | length) pass, \([.[] | select(.Action == "fail")] | length) fail, \([.[] | select(.Action == "skip")] | length) skip"' "$OUT/results.jsonl"
	;;
report)
	normalise
	report
	;;
*)
	echo "usage: $0 [allowlist|full|report]" >&2
	exit 1
	;;
esac
