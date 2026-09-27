#!/bin/sh
set -eu
scenario=${1:-normal}
base=${DEMO_URL:-http://localhost:18080}
curl --max-time 12 -sS -i "$base/checkout?scenario=$scenario" \
  -H 'Content-Type: application/json' \
  -d '{"user_id":1001,"product_id":10,"quantity":2}'
printf '\n'
