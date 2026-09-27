# Verification record

## Phase 1: Checkout without observability

Passed on 2026-09-26 on macOS ARM64 with Docker Engine 29.4.0 and Go 1.27.1.

- `go test ./...` and `go build ./...` passed.
- All five application and database containers became healthy.
- `POST /checkout` returned HTTP 200 and Order 1.
- MySQL contained Quantity 2, Price 100000, and total amount 200000.
- MySQL Stock stayed at 10.
- Redis `cart:1001` contained the submitted User, Product, and Quantity.
- The Trace ID was empty at this phase, as expected before instrumentation.

## Phase 2: Backend HTTP tracing

- `go test ./...`, `go build ./...`, and `go vet ./...` passed.
- The propagation test checked server-to-client-to-server parent links.
- A real Checkout with a supplied W3C parent returned the same Trace ID: `4bf92f3577b34da6a3ce929d0e0e4736`.
- The demo Collector received spans from Cart Service, Product Service, and Order Service with that Trace ID.
- The real functional checks passed again.
- SigNoz was still applying its first-start migrations. This phase proves reception by the demo Collector, not yet storage in SigNoz.

## Phase 3: MySQL and Redis tracing

- Builds and tests passed. A Docker registry TLS timeout required one build retry.
- The functional checks passed against the updated containers.
- SigNoz stored trace `4db34552cdd81b86e519e87d0b59c19f` with all three services.
- Redis `set` and `get` spans had the Cart server span as parent.
- Product SQL spans had the Product server span as parent. Order SQL spans had the Order server span as parent.
- Queries contained parameter placeholders. Redis command arguments were not captured.
- The official SigNoz ingester now uses its generated static configuration. Ingestion works before UI account setup.

## Phase 4: Browser tracing

- Go builds and tests passed; the browser bundle built successfully.
- A real Chromium test checked Checkout, the response Trace ID, `traceparent`, and the Copy Trace ID button.
- SigNoz stored trace `b92bb9a7c340e1fc5bd22a0329fd4f61` with 18 spans across the frontend and all three Go services.
- The test checked that each non-root span has a parent in the same trace. It also checked Product SQL, Order SQL, and Redis GET.
- The test waits for separate browser export batches. The first batch alone does not always contain both browser spans.
- An initial check after a long host suspension lost backend telemetry during export timeouts. A later live check passed. Bounded export queues do not guarantee delivery during an outage.

## Phase 5: Correlated structured logs

- Builds, unit tests, and race tests passed.
- The live functional checks passed after the logging middleware was added.
- Trace `48f58683e193a7488d411a480ecfe20a` had one correlated request log from each service. Each log joined to its service span by native Trace ID and Span ID.
- Cart reused `privacy-check-1001`. Product and Order had separate Request IDs.
- Body fields outside the Checkout model remained visible. Nested passwords and tokens were redacted. Authorization and Cookie headers were absent.
- Malformed JSON returned HTTP 400 with a Trace ID header and did not expose the raw body.
- The log bridge does not directly support `json.Number`. Redaction converts integer values to `int64` and keeps other JSON numbers as text to preserve their value. Trace `d2f2ee0b6152aa412e18bfd0a8c5df52` verified this fix in SigNoz.

## Phase 6: Metrics

- Builds and tests passed, including a metric test for request counts, error counts, Checkout outcomes, and duration observations.
- Live functional checks generated successful and failed Checkouts.
- SigNoz stored the six application metric families: HTTP requests, HTTP errors, HTTP duration, Checkout count, Checkout success, and Checkout failure.
- MySQL connection metrics and Redis client connection metrics were also stored.
- Application metric labels did not contain User, Order, Trace, Span, or Request IDs.

## Phase 7: Failure and latency Scenarios

Builds and race tests passed. Live checks verified span parents, correlated logs, and Order counts for each Scenario.

| Scenario | HTTP status | Observed duration | Trace ID |
|---|---:|---:|---|
| normal | 200 | 26 ms | `18a7e5b378cff199fdc02ff67e0d79f8` |
| slow-product | 200 | 1556 ms | `216eb614ff4be6f0c77b6933ef933155` |
| slow-db | 200 | 1549 ms | `c426ca41d8566ef74945bf81e7e0fca2` |
| redis-slow | 200 | 1525 ms | `4eb4610e02f153b61265273fc7528646` |
| product-error | 502 | 3 ms | `72d56a3d5b8d50d0679f291be02ab0a4` |
| out-of-stock | 409 | 3 ms | `14d6d204bff390d9949434bfed1491f2` |
| order-error | 502 | 9 ms | `cd08da1ecf35013e3c496764c0cffb0e` |

- The slow Product server span, MySQL `SELECT SLEEP(1.5)` span, and delayed Redis GET span each exceeded 1400 ms.
- `order-error` caused a real foreign key rejection in MySQL. No Order was stored.
- `product-error` and `out-of-stock` did not call Order Service.
- Normal Checkout had no error spans.
- All listed sensitive body fields were redacted in stored logs. Unknown non-sensitive fields remained visible. Sensitive headers and raw bodies were absent from traces.
- An unknown Scenario returned HTTP 400 with `INVALID_SCENARIO`.

## Phase 8: Run guide and final checks

Checked on 2026-09-27.

- `make up` passed with the final Foundry startup dependencies and readiness probes.
- `make test` passed: build, Go race tests, and vet.
- The browser test exposed a span lifetime issue: the automatic click span ended before the fetch result. A child `Checkout` span now covers the full asynchronous operation. Its error status is checked after ingestion.
- A browser Checkout produced 19 stored spans with valid parent links: `eeee1e762b7c09d5b0550a46cd191a7d`.
- A failed browser Checkout had an error Checkout span and visible Trace ID: `fb932f8065cb75f6da8af3df3fc49cfb`.
- With the demo Collector stopped, Checkout still returned HTTP 200 in 0.26 seconds. The test restored the Collector and passed readiness probes.
- The README includes architecture diagrams, startup and stop commands, all seven Scenarios, and the Observability Demo Walkthrough.

The browser checks use a real headless Chromium browser. Telemetry checks query the real SigNoz ClickHouse tables. They do not substitute mocks for application storage or export. SigNoz account setup and manual UI navigation are not part of these automated checks.

Final checks after Collector recovery also passed:

- All seven Scenarios passed again; delayed operations took 1513–1524 ms.
- Normal trace: `c7f275aba4f63e1179a985852fbdb5f4`.
- Browser normal trace: `c5c46b15eb22da41d7c0c94d65aad504`; 19 spans, with no orphan parents. Its full Checkout span took 45.2 ms and its Cart span took 23.5 ms.
- Browser error trace: `f41877b1ef05805166e0dfc3dabea6c0`.
- Recent metric samples contained all six application metric families. Stored series labels used only the bounded allowlist. Series metadata timestamps are bucketed; the check uses sample timestamps for freshness.
- A submitted Price of 1 did not change stored Price 100000 or total amount 200000. Stock remained 10.
- Compose configuration and shell syntax checks passed. All long-running containers were running; migration and helper jobs exited successfully.
- The required `graphify update .` completed after code edits.
