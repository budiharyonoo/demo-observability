package demo

import (
	"context"
	"errors"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

type appMetrics struct {
	requests, errors, checkouts, successes, failures metric.Int64Counter
	duration                                         metric.Float64Histogram
}

func newMetrics(meter metric.Meter) (*appMetrics, error) {
	requests, e1 := meter.Int64Counter("demo.http.requests", metric.WithDescription("Completed application HTTP requests"))
	duration, e2 := meter.Float64Histogram("demo.http.duration", metric.WithUnit("s"), metric.WithExplicitBucketBoundaries(.005, .01, .025, .05, .1, .25, .5, 1, 2, 5, 10))
	failuresHTTP, e3 := meter.Int64Counter("demo.http.errors", metric.WithDescription("Application HTTP responses with status 400 or higher"))
	checkouts, e4 := meter.Int64Counter("demo.checkout.count", metric.WithDescription("Checkout attempts, including invalid requests"))
	successes, e5 := meter.Int64Counter("demo.checkout.success")
	failures, e6 := meter.Int64Counter("demo.checkout.failure")
	return &appMetrics{requests: requests, duration: duration, errors: failuresHTTP, checkouts: checkouts, successes: successes, failures: failures}, errors.Join(e1, e2, e3, e4, e5, e6)
}
func (m *appMetrics) record(ctx context.Context, method, route string, status int, duration time.Duration) {
	attrs := metric.WithAttributes(attribute.String("http.request.method", method), attribute.String("http.route", route), attribute.Int("http.response.status_code", status))
	m.requests.Add(ctx, 1, attrs)
	m.duration.Record(ctx, duration.Seconds(), attrs)
	if status >= 400 {
		m.errors.Add(ctx, 1, attrs)
	}
	if route == "/checkout" {
		m.checkouts.Add(ctx, 1)
		if status < 400 {
			m.successes.Add(ctx, 1)
		} else {
			m.failures.Add(ctx, 1)
		}
	}
}
