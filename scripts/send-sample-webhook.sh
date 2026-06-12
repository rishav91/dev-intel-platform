#!/usr/bin/env bash
# Sends the sample pull_request payload to the local webhook-gateway with a
# valid GitHub HMAC-SHA256 signature. Demonstrates the full Phase 0 spine.
#
#   GITHUB_WEBHOOK_SECRET=dev-secret ./scripts/send-sample-webhook.sh
#
set -euo pipefail

SECRET="${GITHUB_WEBHOOK_SECRET:-dev-secret}"
URL="${GATEWAY_URL:-http://localhost:8080/webhooks/github}"
PAYLOAD_FILE="$(dirname "$0")/sample-pull-request.json"

body="$(cat "$PAYLOAD_FILE")"
sig="sha256=$(printf '%s' "$body" | openssl dgst -sha256 -hmac "$SECRET" | awk '{print $2}')"
delivery="$(uuidgen 2>/dev/null || python3 -c 'import uuid;print(uuid.uuid4())')"

echo "POST $URL  (delivery=$delivery)"
curl -sS -X POST "$URL" \
  -H "Content-Type: application/json" \
  -H "X-GitHub-Event: pull_request" \
  -H "X-GitHub-Delivery: $delivery" \
  -H "X-Hub-Signature-256: $sig" \
  --data "$body" \
  -w '\nHTTP %{http_code}\n'

echo "Now check the work_item landed (tenant-scoped):"
echo "  psql 'postgres://devintel:devintel@localhost:5432/devintel' -c \\"
echo "    \"SET app.tenant_id='11111111-1111-1111-1111-111111111111'; SELECT repo, number, title, current_stage FROM work_item;\""
