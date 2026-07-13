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

func TestInjectBase(t *testing.T) {
	html := "<html><head></head><body>hi</body></html>"
	out := injectBase(html, "/s/demo/")
	if !contains(out, `<base href="/s/demo/">`) {
		t.Fatalf("missing base: %s", out)
	}
	// do not double-inject
	out2 := injectBase(out, "/s/demo/")
	if stringsCount(out2, "<base") != 1 {
		t.Fatalf("expected one base tag: %s", out2)
	}
}

func TestRewriteRootAbsoluteAssets(t *testing.T) {
	html := `<link href="/css/style.css"><script src="/js/a.js"></script><script src="/sdk.js"></script><a href="/api/x">`
	out := rewriteRootAbsoluteAssets(html, "/s/demo")
	if !contains(out, `href="/s/demo/css/style.css"`) {
		t.Fatalf("css not rewritten: %s", out)
	}
	if !contains(out, `src="/s/demo/js/a.js"`) {
		t.Fatalf("js not rewritten: %s", out)
	}
	if !contains(out, `src="/sdk.js"`) {
		t.Fatalf("sdk.js should stay apex: %s", out)
	}
	if !contains(out, `href="/api/x"`) {
		t.Fatalf("api should stay: %s", out)
	}
}

func stringsCount(s, sub string) int {
	n, i := 0, 0
	for {
		j := indexAt(s, sub, i)
		if j < 0 {
			return n
		}
		n++
		i = j + len(sub)
	}
}

func indexAt(s, sub string, start int) int {
	if start >= len(s) {
		return -1
	}
	idx := -1
	rest := s[start:]
	for i := 0; i+len(sub) <= len(rest); i++ {
		if rest[i:i+len(sub)] == sub {
			idx = start + i
			break
		}
	}
	return idx
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
