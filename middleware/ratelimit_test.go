package middleware

import (
	"testing"
	"time"
)

func TestRateLimiter(t *testing.T) {
	rl := NewRateLimiter(3, time.Minute)
	for i := 0; i < 3; i++ {
		if !rl.Allow("k") {
			t.Fatalf("expected allow on %d", i)
		}
	}
	if rl.Allow("k") {
		t.Fatal("expected deny on 4th")
	}
	if !rl.Allow("other") {
		t.Fatal("other key should allow")
	}
}
