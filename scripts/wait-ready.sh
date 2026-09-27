#!/bin/sh
set -eu
# Probe from the Compose network. Collector images have no shell or wget.
for attempt in $(seq 1 60); do
  if docker compose exec -T frontend sh -ec '
    wget -q -O /dev/null http://cart-service:8080/healthz
    wget -q -O /dev/null http://otel-collector:13133/
    wget -q -O /dev/null http://ingester:13133/
    wget -q -O /dev/null http://demo-signoz-signoz-0:8080/api/v1/health
  ' >/dev/null 2>&1; then
    echo 'Ready: Checkout, both Collectors, and SigNoz'
    exit 0
  fi
  sleep 2
done
echo 'Startup check failed. Run docker compose ps -a and docker compose logs.' >&2
exit 1
