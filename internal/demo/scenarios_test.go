package demo

import (
	"context"
	"errors"
	"github.com/redis/go-redis/v9"
	"testing"
	"time"
)

func TestScenariosAndCancellation(t *testing.T) {
	for _, value := range []string{"normal", "slow-product", "slow-db", "redis-slow", "product-error", "out-of-stock", "order-error"} {
		if parseScenario(value) != value {
			t.Fatal(value)
		}
	}
	if parseScenario("") != "normal" || parseScenario("arbitrary-input") != "invalid" {
		t.Fatal("scenario validation")
	}
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), scenarioKey{}, "redis-slow"))
	cancel()
	called := false
	hook := redisDelayHook{}.ProcessHook(func(context.Context, redis.Cmder) error { called = true; return nil })
	start := time.Now()
	err := hook(ctx, redis.NewStringCmd(ctx, "get", "cart:1001"))
	if !errors.Is(err, context.Canceled) || called || time.Since(start) > time.Second {
		t.Fatal("Redis delay ignored cancellation")
	}
}
