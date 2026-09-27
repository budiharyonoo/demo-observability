package demo

import (
	"context"
	"errors"
	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

func startTelemetry(ctx context.Context, service string) (func(), error) {
	stdout := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{ReplaceAttr: func(_ []string, attr slog.Attr) slog.Attr {
		if attr.Key == slog.TimeKey {
			attr.Key = "timestamp"
		}
		if attr.Key == slog.LevelKey {
			attr.Value = slog.StringValue(strings.ToLower(attr.Value.String()))
		}
		return attr
	}})
	local := slog.New(stdout).With("service.name", service)
	slog.SetDefault(local)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
		local.Error("telemetry export failed", "error.type", "TELEMETRY_EXPORT", "error.message", err.Error())
	}))
	if os.Getenv("OTEL_SDK_DISABLED") == "true" {
		return func() {}, nil
	}
	res := resource.NewSchemaless(attribute.String("service.name", service), attribute.String("service.version", "1.0.0"), attribute.String("deployment.environment.name", "local"))
	exporter, err := otlptracegrpc.New(ctx, otlptracegrpc.WithTimeout(2*time.Second))
	if err != nil {
		return nil, err
	}
	provider := sdktrace.NewTracerProvider(sdktrace.WithResource(res), sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.AlwaysSample())), sdktrace.WithBatcher(exporter, sdktrace.WithMaxQueueSize(512), sdktrace.WithBatchTimeout(time.Second), sdktrace.WithExportTimeout(2*time.Second)))
	otel.SetTracerProvider(provider)
	logExporter, err := otlploggrpc.New(ctx, otlploggrpc.WithTimeout(2*time.Second))
	if err != nil {
		_ = provider.Shutdown(ctx)
		return nil, err
	}
	logProvider := sdklog.NewLoggerProvider(sdklog.WithResource(res), sdklog.WithProcessor(sdklog.NewBatchProcessor(logExporter, sdklog.WithMaxQueueSize(512), sdklog.WithExportInterval(time.Second), sdklog.WithExportTimeout(2*time.Second))))
	slog.SetDefault(slog.New(slog.NewMultiHandler(stdout, otelslog.NewHandler(service, otelslog.WithLoggerProvider(logProvider)))).With("service.name", service))
	metricExporter, err := otlpmetricgrpc.New(ctx, otlpmetricgrpc.WithTimeout(2*time.Second))
	if err != nil {
		_ = provider.Shutdown(ctx)
		_ = logProvider.Shutdown(ctx)
		return nil, err
	}
	metricProvider := sdkmetric.NewMeterProvider(sdkmetric.WithResource(res), sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExporter, sdkmetric.WithInterval(5*time.Second), sdkmetric.WithTimeout(2*time.Second))))
	otel.SetMeterProvider(metricProvider)
	return func() {
		shutdown, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		_ = provider.Shutdown(shutdown)
		_ = logProvider.Shutdown(shutdown)
		_ = metricProvider.Shutdown(shutdown)
	}, nil
}
func traceID(ctx context.Context) string {
	sc := trace.SpanContextFromContext(ctx)
	if !sc.IsValid() {
		return ""
	}
	return sc.TraceID().String()
}
func recordFailure(ctx context.Context, reason string) {
	span := trace.SpanFromContext(ctx)
	span.RecordError(errors.New(reason))
	span.SetStatus(codes.Error, reason)
	span.SetAttributes(attribute.String("error.type", reason))
}
func (a *App) route(name string, handler http.HandlerFunc) http.Handler {
	logged := a.requestLog(name, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if scenario(r.Context()) == "invalid" {
			fail(w, r, 400, "INVALID_SCENARIO")
			return
		}
		handler(w, r)
	}))
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(context.WithValue(r.Context(), scenarioKey{}, parseScenario(r.URL.Query().Get("scenario"))), 6*time.Second)
		defer cancel()
		r = r.WithContext(ctx)
		trace.SpanFromContext(ctx).SetAttributes(attribute.String("demo.scenario", scenario(ctx)))
		w.Header().Set("X-Trace-Id", traceID(r.Context()))
		w.Header().Set("Access-Control-Expose-Headers", "X-Trace-Id, X-Request-Id")
		logged.ServeHTTP(w, r)
	})
	return otelhttp.NewHandler(inner, name, otelhttp.WithSpanOptions(trace.WithAttributes(attribute.String("http.route", name))))
}
