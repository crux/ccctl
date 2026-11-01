#!/usr/bin/env bash
# Builds the made-up world for the demos under /home/dev: project
# directories, session files with titles and conversations, and one
# large project (about 1 GB) so that search has something to scan.
set -euo pipefail
export HOME=/home/dev
P=$HOME/.claude/projects
mkdir -p "$P" "$HOME/.claude/sessions"
cp /demo/zshrc /demo/.zshrc
. /demo/sessions.sh

# enc DIR: Claude Code's folder name for a directory.
enc() { printf '%s' "$1" | sed 's#[/. @]#-#g'; }

# uuid SEED: a stable, random-looking session ID.
uuid() {
	local h
	h=$(printf '%s' "$1" | md5sum | cut -c1-32)
	printf '%s-%s-4%s-a%s-%s' "${h:0:8}" "${h:8:4}" "${h:13:3}" "${h:17:3}" "${h:20:12}"
}

# session ID CWD TITLE AGE TEXT...: one session file with alternating
# user and assistant turns, the AI title at the end, and AGE as mtime.
session() {
	local id=$1 cwd=$2 title=$3 age=$4 role=user f ts
	shift 4
	mkdir -p "$P/$(enc "$cwd")" "$cwd"
	f="$P/$(enc "$cwd")/$id.jsonl"
	ts=$(date -u -d "$age" +%Y-%m-%dT%H:%M:%SZ)
	: > "$f"
	for text in "$@"; do
		if [ "$role" = user ]; then
			printf '{"type":"user","sessionId":"%s","cwd":"%s","timestamp":"%s","message":{"role":"user","content":"%s"}}\n' "$id" "$cwd" "$ts" "$text" >> "$f"
			role=assistant
		else
			printf '{"type":"assistant","sessionId":"%s","cwd":"%s","timestamp":"%s","message":{"role":"assistant","content":[{"type":"text","text":"%s"}]}}\n' "$id" "$cwd" "$ts" "$text" >> "$f"
			role=user
		fi
	done
	printf '{"type":"ai-title","aiTitle":"%s","sessionId":"%s"}\n' "$title" "$id" >> "$f"
	touch -d "$age" "$f"
}

# --- running sessions (start.sh puts them into tmux windows)
IFS='|' read -r _ id cwd title _ <<< "${RUNNING[0]}"
session "$id" "$cwd" "$title" "4 minutes ago" \
	"The checkout total is off by one cent for some carts with VAT. Can you find out why?" \
	"The VAT is rounded per line item and again on the total. I will round once, on the total, and add a test with the three carts from the bug report."
IFS='|' read -r _ id cwd title _ <<< "${RUNNING[1]}"
session "$id" "$cwd" "$title" "12 minutes ago" \
	"We need a rate limit on the public API: 100 requests per minute per key." \
	"I will add a token bucket per API key in the middleware, and send a Retry-After header when a client hits the rate limit."
IFS='|' read -r _ id cwd title _ <<< "${RUNNING[2]}"
session "$id" "$cwd" "$title" "40 minutes ago" \
	"Move the Terraform state from local files to an S3 backend with locking." \
	"Plan: create the bucket and the DynamoDB lock table, then run terraform init -migrate-state."

# --- earlier sessions
session "$(uuid webshop-stripe)" "$HOME/src/webshop" "Stripe webhook retries" "2 days ago" \
	"Stripe retries webhooks after a timeout and we create duplicate orders." \
	"I will make the handler idempotent: store the event ID and answer 200 for events we have seen."
session "$(uuid webshop-search)" "$HOME/src/webshop" "Product search with Postgres full-text" "6 days ago" \
	"Search should find running shoes when someone types run shoe." \
	"Postgres full-text search with prefix matching covers that. No extra search engine needed."
session "$(uuid api-jwt)" "$HOME/src/api-gateway" "JWT key rotation without logouts" "9 days ago" \
	"How do we rotate the JWT signing key without logging everyone out?" \
	"Publish the new key in the JWKS first, sign with it a day later, and drop the old key after the token lifetime. Clients that hit the rate limit during the switch get a 429, not a 401."
session "$(uuid mobile-push)" "$HOME/src/mobile-app" "Push notifications on iOS 18" "14 days ago" \
	"Push notifications stopped arriving on iOS 18 test devices." \
	"The APNs topic still uses the old bundle ID. I will update it and send a test push."
session "$(uuid notes-outage)" "$HOME/notes" "Blog post: what the outage taught us" "20 days ago" \
	"Help me outline a blog post about last week's outage." \
	"Timeline, root cause (a missing rate limit on an internal endpoint), what we changed, and what we still do not know."

# --- a large project: 440 sessions of about 2.3 MB each
cwd=$HOME/src/platform
dir="$P/$(enc "$cwd")"
mkdir -p "$dir" "$cwd"
tmpl=$(mktemp)
log=$(for i in $(seq 1 90); do printf 'INFO build step %04d ok in 12ms, cache hit, artefact sha256:9f2c41e1 stored\\n' "$i"; done)
for i in $(seq 1 300); do
	printf '{"type":"user","sessionId":"SID","cwd":"CWD","message":{"role":"user","content":[{"type":"tool_result","content":"%s"}]}}\n' "$log"
	printf '{"type":"assistant","sessionId":"SID","cwd":"CWD","message":{"role":"assistant","content":[{"type":"text","text":"The build is green again. Next: the flaky integration test in payments."}]}}\n'
done > "$tmpl"
titles=("Flaky integration test in payments" "Upgrade to Go 1.26" "Cache warm-up after deploys" "Split the monolith: orders service"
	"Reduce Docker image size" "OpenTelemetry traces for checkout" "Database migration for invoices" "CI runs twice per push"
	"Feature flags for the new pricing" "Memory leak in the import worker")
for n in $(seq 1 440); do
	id=$(uuid "platform-$n")
	f="$dir/$id.jsonl"
	sed "s/SID/$id/g; s#CWD#$cwd#g" "$tmpl" > "$f"
	case $n in
	17 | 203 | 388)
		printf '{"type":"user","sessionId":"%s","cwd":"%s","message":{"role":"user","content":"The import worker hits the rate limit of the partner API at night."}}\n' "$id" "$cwd" >> "$f"
		;;
	esac
	printf '{"type":"ai-title","aiTitle":"%s","sessionId":"%s"}\n' "${titles[$((n % 10))]}" "$id" >> "$f"
	touch -d "$((n / 4 + 21)) days ago" "$f"
done
echo "setup: $(du -sh "$P" | cut -f1) in $(find "$P" -name '*.jsonl' | wc -l) sessions"
