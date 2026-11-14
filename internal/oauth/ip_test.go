package oauth

import "testing"

func TestLimitKey(t *testing.T) {
	a := LimitKey("2001:db8:1:2:aaaa::1")
	if a != "2001:db8:1:2::/64" || a != LimitKey("2001:db8:1:2:bbbb::9") {
		t.Errorf("v6: %q", a)
	}
	if LimitKey("2001:db8:1:3::1") == a {
		t.Error("/64 diferentes devem diferir")
	}
	if got := LimitKey("203.0.113.5"); got != "203.0.113.5" {
		t.Errorf("v4: %q", got)
	}
	if got := LimitKey("::ffff:203.0.113.5"); got != "203.0.113.5" {
		t.Errorf("v4 mapeado: %q", got)
	}
	if got := LimitKey("lixo"); got != "lixo" {
		t.Errorf("inválido: %q", got)
	}
}
