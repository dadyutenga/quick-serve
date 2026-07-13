package middleware

import "testing"

func TestGenerateAndVerifyToken(t *testing.T) {
	tok, err := GenerateToken()
	if err != nil {
		t.Fatal(err)
	}
	if len(tok) < 20 {
		t.Fatalf("token too short: %s", tok)
	}
	hash, err := HashToken(tok)
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyToken(tok, hash) {
		t.Fatal("expected token to verify")
	}
	if VerifyToken("wrong", hash) {
		t.Fatal("wrong token should not verify")
	}
	if VerifyToken("", hash) {
		t.Fatal("empty token should not verify")
	}
}
