package oauth

import "testing"

func TestRedirectAllowed(t *testing.T) {
	ok := []string{
		"http://127.0.0.1:33418/",
		"http://localhost:7777/oauth/callback",
		"http://[::1]:5000/cb",
		"http://localhost/callback",
		"https://vscode.dev/redirect",
		"https://insiders.vscode.dev/redirect",
	}
	bad := []string{
		"http://localhost.evil.com/cb",
		"http://127.0.0.1.nip.io/cb",
		"http://a@127.0.0.1/cb",
		"http://a:b@127.0.0.1/cb",
		"http://127.0.0.1/cb?x=1",
		"http://127.0.0.1/cb?",
		"http://127.0.0.1/cb#f",
		"https://127.0.0.1/cb",
		"https://localhost/cb",
		"https://vscode.dev/redirect/../x",
		"https://vscode.dev/redirect/",
		"https://vscode.dev/redirect?x=1",
		"https://vscode.dev/redirect#f",
		"https://vscode.dev:444/redirect",
		"http://vscode.dev/redirect",
		"https://vscode.dev.evil.com/redirect",
		"vscode://ext.id/cb",
		"vscode-insiders://x/cb",
		"cursor://anysphere.cursor-deeplink/mcp/install",
		"javascript:alert(1)",
		"",
		"http://192.168.0.1/cb",
		"http://127.0.0.1/../cb",
		"http://127.0.0.1/a/../cb",
		"http://127.0.0.1/a/./cb",
		"http://127.0.0.1//cb",
		"http://127.0.0.1/%2e%2e/cb",
		"http://127.0.0.1:99999/cb",
		"http://127.0.0.1:abc/cb",
		"http://127.0.0.1/cb/",
		"http://127.0.0.1",
		"http://LOCALHOST.evil.com/cb",
		" http://127.0.0.1/cb",
		"http://127.0.0.1/cb\n",
		"/cb",
	}
	for _, u := range ok {
		if !RedirectAllowed(u) {
			t.Errorf("deveria aceitar %q", u)
		}
	}
	for _, u := range bad {
		if RedirectAllowed(u) {
			t.Errorf("deveria recusar %q", u)
		}
	}
}

func TestRedirectMatches(t *testing.T) {
	cases := []struct {
		reg, req string
		want     bool
	}{
		{"http://127.0.0.1:1/cb", "http://127.0.0.1:5555/cb", true},
		{"http://127.0.0.1:1/cb", "http://127.0.0.1:1/cb", true},
		{"http://127.0.0.1:1/cb", "http://127.0.0.1:5555/cb2", false},
		{"http://127.0.0.1:1/cb", "http://localhost:1/cb", false},
		{"http://127.0.0.1:1/cb", "http://127.0.0.1.nip.io:1/cb", false},
		{"http://localhost:1/cb", "http://localhost:2/cb", true},
		{"http://[::1]:1/cb", "http://[::1]:2/cb", true},
		{"http://127.0.0.1:1/cb", "http://127.0.0.1:2/cb?x=1", false},
		{"https://vscode.dev/redirect", "https://vscode.dev/redirect", true},
		{"https://vscode.dev/redirect", "https://insiders.vscode.dev/redirect", false},
		{"https://vscode.dev/redirect", "https://vscode.dev/redirect/", false},
		{"https://vscode.dev/redirect", "https://vscode.dev:443/redirect", false},
		{"https://evil.com/x", "https://evil.com/x", false},
		{"", "", false},
	}
	for _, c := range cases {
		if got := RedirectMatches(c.reg, c.req); got != c.want {
			t.Errorf("RedirectMatches(%q,%q)=%v, quer %v", c.reg, c.req, got, c.want)
		}
	}
}

func TestDescribeRedirect(t *testing.T) {
	cases := map[string]string{
		"http://127.0.0.1:33418/":              "este computador (127.0.0.1:33418)",
		"http://localhost:7777/cb":             "este computador (localhost:7777)",
		"http://[::1]:5000/cb":                 "este computador ([::1]:5000)",
		"http://localhost/cb":                  "este computador",
		"https://vscode.dev/redirect":          "VS Code (vscode.dev)",
		"https://insiders.vscode.dev/redirect": "VS Code Insiders (vscode.dev)",
	}
	for in, want := range cases {
		if got := DescribeRedirect(in); got != want {
			t.Errorf("DescribeRedirect(%q)=%q, quer %q", in, got, want)
		}
	}
}
