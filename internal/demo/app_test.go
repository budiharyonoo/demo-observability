package demo

import (
	"math"
	"testing"
)

func TestTotal(t *testing.T) {
	for _, tt := range []struct {
		c    Checkout
		want int64
		bad  bool
	}{
		{Checkout{1001, 10, 2, 100000}, 200000, false},
		{Checkout{1001, 10, 0, 100000}, 0, true},
		{Checkout{1001, 10, 2, math.MaxInt64}, 0, true},
		{Checkout{0, 10, 2, 100000}, 0, true},
	} {
		got, err := total(tt.c)
		if (err != nil) != tt.bad || got != tt.want {
			t.Fatalf("total(%+v) = %d, %v", tt.c, got, err)
		}
	}
}
