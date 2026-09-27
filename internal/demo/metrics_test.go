package demo

import (
	"context"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"testing"
	"time"
)

func TestMetrics(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	defer provider.Shutdown(context.Background())
	metrics, err := newMetrics(provider.Meter("test"))
	if err != nil {
		t.Fatal(err)
	}
	metrics.record(context.Background(), "POST", "/checkout", 200, 100*time.Millisecond)
	metrics.record(context.Background(), "POST", "/checkout", 409, 200*time.Millisecond)
	var data metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &data); err != nil {
		t.Fatal(err)
	}
	sums := map[string]int64{}
	for _, scope := range data.ScopeMetrics {
		for _, measurement := range scope.Metrics {
			switch value := measurement.Data.(type) {
			case metricdata.Sum[int64]:
				for _, point := range value.DataPoints {
					sums[measurement.Name] += point.Value
					for _, attr := range point.Attributes.ToSlice() {
						switch string(attr.Key) {
						case "http.route", "http.request.method", "http.response.status_code":
						default:
							t.Fatalf("unexpected metric label: %s", attr.Key)
						}
					}
				}
			case metricdata.Histogram[float64]:
				var count uint64
				for _, point := range value.DataPoints {
					count += point.Count
				}
				if count != 2 {
					t.Fatalf("duration count %d", count)
				}
			}
		}
	}
	for name, want := range map[string]int64{"demo.http.requests": 2, "demo.http.errors": 1, "demo.checkout.count": 2, "demo.checkout.success": 1, "demo.checkout.failure": 1} {
		if sums[name] != want {
			t.Fatalf("%s = %d", name, sums[name])
		}
	}
}
