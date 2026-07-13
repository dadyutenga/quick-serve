package routes

import "testing"

func TestClampMaxTokens(t *testing.T) {
	if got := ClampMaxTokens(0, 1024); got != 512 {
		t.Fatalf("default: got %d", got)
	}
	if got := ClampMaxTokens(9999, 1024); got != 1024 {
		t.Fatalf("clamp: got %d", got)
	}
	if got := ClampMaxTokens(100, 1024); got != 100 {
		t.Fatalf("passthrough: got %d", got)
	}
}
