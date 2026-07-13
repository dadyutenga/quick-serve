package middleware

import "testing"

func TestIsAllowedOrigin(t *testing.T) {
	base := "quick.dadyprojects.tech"
	if !IsAllowedOrigin("https://foo.quick.dadyprojects.tech", base) {
		t.Fatal("subdomain should allow")
	}
	if !IsAllowedOrigin("http://localhost:8080", base) {
		t.Fatal("localhost should allow")
	}
	if IsAllowedOrigin("https://evil.example.com", base) {
		t.Fatal("foreign origin should deny")
	}
}
