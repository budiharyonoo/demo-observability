# Compatibility review

Checked on 2026-09-26 before instrumentation code was added.

| Component | Selected version | Reason |
|---|---|---|
| Go | 1.27.1 | Current stable Go release; also installed locally |
| OpenTelemetry Go | 1.46.0 | Stable traces and metrics |
| `otelhttp` | 0.71.0 | Official HTTP server and client instrumentation |
| `XSAM/otelsql` | 0.44.0 | Maintained `database/sql` instrumentation; supports MySQL and connection metrics |
| `go-redis` and `redisotel/v9` | 9.22.0 | Redis client and its tracing instrumentation use matching versions |
| OpenTelemetry Go logs SDK | 0.22.0 | Logs API and SDK are release candidate; pin versions |
| `otelslog` | 0.20.1 | Official bridge from Go structured logging to OpenTelemetry |
| OpenTelemetry Web SDK | 2.11.0 | Browser spans with a compatible context manager |
| Web OTLP exporter and fetch instrumentation | 0.222.0 | Match the SDK release series |
| User interaction instrumentation | 0.66.0 | Browser click spans |
| SigNoz | 0.143.0 | Pinned self-hosted release |
| SigNoz Collector | 0.144.12 | Pinned ingestion and schema migration image |
| Foundry | 0.3.0 | Official Compose generator; generated files are included |

The Go module versions require Go 1.25 or later. Go 1.27.1 meets that requirement. Go module resolution and the browser lockfile must be checked by builds and live tests.

## Compatibility risks

- The old SigNoz `deploy/` Compose distribution is deprecated. Use Foundry to generate the official components. Keep the casting file, lockfile, generated configuration, and license together.
- Browser instrumentation has different stability guarantees from backend instrumentation. Use a matching SDK and exporter, bundle dependencies locally, and test context propagation in a real browser. Do not rely on a CDN at runtime.
- Use a Zone context manager with user interaction instrumentation. Keep fetch inside the active click context. Do not instrument telemetry export requests.
- Redis command text can contain Cart data. Disable command text capture. SQL uses parameterized queries and must not record parameter values.
- Go logs use separately versioned modules. Use a compatible `otelslog` bridge. Pass the request context to logging so the OTLP record has native Trace ID and Span ID fields.
- Use OTLP/gRPC from Go to the demo Collector, then to the SigNoz ingester. Browser export uses OTLP/HTTP through the frontend proxy.
- The Docker runtime currently has about 4 GB of memory and uses ARM64. SigNoz needs at least 4 GB by itself. Check startup and memory use; recommend 8 GB or more for the complete demo.

## Sources

- [Go releases](https://go.dev/dl/)
- [OpenTelemetry Go status](https://opentelemetry.io/docs/languages/go/)
- [OpenTelemetry browser guide](https://opentelemetry.io/docs/languages/js/getting-started/browser/)
- [SQL instrumentation](https://github.com/XSAM/otelsql)
- [Redis instrumentation](https://github.com/redis/go-redis#instrument-with-opentelemetry)
- [SigNoz Docker installation](https://signoz.io/docs/install/docker/)
- [Foundry patches](https://github.com/SigNoz/foundry/blob/v0.3.0/docs/concepts/patches.md)

This review selects packages. The phase record in `verification.md` records what was actually tested.

## Verified Foundry adjustments

The generated ingester uses OpAMP management. Before the first SigNoz organization exists, its effective configuration has `nop` pipelines. This demo starts the ingester with the generated static configuration and no `--manager-config`. This avoids the first-account dependency for ingestion. The customization is recorded as a Foundry patch; the generated file is not edited by hand. Remote Collector management in the SigNoz UI is not used.

The demo Collector uses `otlp_grpc/signoz`. Collector 0.161.0 warns that the older `otlp` exporter alias is deprecated. The OTLP receiver keeps its `otlp` name.

## Browser span lifetime

The standard user interaction span can end when the click handler returns, before the fetch result is handled. A custom `Checkout` span therefore covers the asynchronous operation and records its outcome. It remains a child of the standard click span. Fetch and backend spans remain in the same trace. The real Chromium tests check both successful and failed Checkouts.

The final Compose patches pin PostgreSQL to 16.15 and order the ingester and SigNoz startup after successful schema migration. The application Collector no longer duplicates traces to the debug exporter.
