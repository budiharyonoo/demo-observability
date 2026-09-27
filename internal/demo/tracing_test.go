package demo

import (
	"context"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTTPPropagation(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	old := otel.GetTracerProvider()
	oldProp := otel.GetTextMapPropagator()
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	defer func() {
		otel.SetTracerProvider(old)
		otel.SetTextMapPropagator(oldProp)
		_ = provider.Shutdown(context.Background())
	}()
	a := &App{Client: &http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport)}}
	downstream := httptest.NewServer(requestIDMiddleware(a.route("/orders", func(w http.ResponseWriter, r *http.Request) {
		respond(w, 201, Result{Success: true, TraceID: traceID(r.Context())})
	})))
	defer downstream.Close()
	upstream := httptest.NewServer(requestIDMiddleware(a.route("/checkout", func(w http.ResponseWriter, r *http.Request) {
		var result Result
		if _, err := a.call(r.Context(), "POST", downstream.URL, map[string]int{"quantity": 2}, &result); err != nil {
			t.Error(err)
		}
		respond(w, 200, result)
	})))
	defer upstream.Close()
	req, _ := http.NewRequest("GET", upstream.URL, nil)
	const want = "4bf92f3577b34da6a3ce929d0e0e4736"
	req.Header.Set("traceparent", "00-"+want+"-00f067aa0ba902b7-01")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.Header.Get("X-Trace-Id") != want {
		t.Fatalf("wrong Trace ID: %v", res.Header)
	}
	spans := recorder.Ended()
	if len(spans) != 3 {
		t.Fatalf("got %d spans", len(spans))
	}
	for _, span := range spans {
		if span.SpanContext().TraceID().String() != want {
			t.Fatal("trace split")
		}
		if !span.Parent().IsValid() {
			t.Fatal("missing parent")
		}
	}
	if traceID(context.Background()) != "" {
		t.Fatal("invalid context returned a Trace ID")
	}
}
