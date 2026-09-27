.PHONY: up down logs test test-browser verify verify-outage
up:
	docker compose up -d --build --wait --wait-timeout 600
	./scripts/wait-ready.sh
down:
	docker compose down
logs:
	docker compose logs -f cart-service product-service order-service otel-collector
test:
	go build ./...
	go test -race ./...
	go vet ./...
test-browser:
	cd frontend && npm test
verify:
	./scripts/wait-ready.sh
	python3 scripts/verify-functional.py
	python3 scripts/verify-telemetry.py
verify-outage:
	python3 scripts/verify-outage.py
demo-%:
	./scripts/test-demo.sh $*
