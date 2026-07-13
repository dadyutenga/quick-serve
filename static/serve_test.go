package static

import "testing"

func TestIsQuickControlPath(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{".quick_site_token", true},
		{"assets/.quick_site_token", true},
		{"foo/.quick/bar", true},
		{"index.html", false},
		{"assets/logo.png", false},
		{"quick_site_token", false},
	}
	for _, tc := range cases {
		if got := isQuickControlPath(tc.path); got != tc.want {
			t.Errorf("isQuickControlPath(%q)=%v want %v", tc.path, got, tc.want)
		}
	}
}

func TestInjectToken(t *testing.T) {
	html := "<html><head></head><body>hi</body></html>"
	out := injectToken(html, "abc")
	if out == html {
		t.Fatal("expected injection")
	}
	if !contains(out, `__QUICK_TOKEN__="abc"`) {
		t.Fatalf("missing token: %s", out)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		(func() bool {
			for i := 0; i+len(sub) <= len(s); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
			return false
		})())
}
