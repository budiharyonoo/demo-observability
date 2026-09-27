package demo

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

type scenarioKey struct{}

func parseScenario(value string) string {
	switch value {
	case "":
		return "normal"
	case "normal", "slow-product", "slow-db", "redis-slow", "product-error", "out-of-stock", "order-error":
		return value
	default:
		return "invalid"
	}
}
func scenario(ctx context.Context) string {
	value, _ := ctx.Value(scenarioKey{}).(string)
	if value == "" {
		return "normal"
	}
	return value
}
func delay(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// The tracing hook wraps this hook, so the injected delay belongs to the GET span.
// This simulates client operation latency; it does not make the Redis server slow.
type redisDelayHook struct{}

func (redisDelayHook) DialHook(next redis.DialHook) redis.DialHook { return next }
func (redisDelayHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}
func (redisDelayHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if cmd.Name() == "get" && scenario(ctx) == "redis-slow" {
			trace.SpanFromContext(ctx).SetAttributes(attribute.Bool("demo.delay.injected", true), attribute.Int("demo.delay.ms", 1500))
			if err := delay(ctx, 1500*time.Millisecond); err != nil {
				return err
			}
		}
		return next(ctx, cmd)
	}
}
