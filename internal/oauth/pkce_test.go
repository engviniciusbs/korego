package oauth

import (
	"strings"
	"testing"
)

func TestVerifyPKCE(t *testing.T) {
	const v = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	const c = "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"
	if !VerifyPKCE(v, c) {
		t.Error("vetor da RFC 7636 deveria passar")
	}
	if VerifyPKCE(v[:42], c) {
		t.Error("verifier de 42 caracteres deveria falhar")
	}
	if VerifyPKCE(v, "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cX") {
		t.Error("challenge errado deveria falhar")
	}
	if VerifyPKCE(v+strings.Repeat("a", 90), c) {
		t.Error("verifier acima de 128 deveria falhar")
	}
	if VerifyPKCE(v[:42]+"+", c) || VerifyPKCE(v[:42]+"=", c) {
		t.Error("caracteres fora do conjunto deveriam falhar")
	}
	if VerifyPKCE("", "") {
		t.Error("vazio deveria falhar")
	}
}

func TestNormalizeResource(t *testing.T) {
	got, ok := NormalizeResource("HTTPS://Painel.Exemplo.com:443/mcp/")
	if !ok || got != "https://painel.exemplo.com/mcp" {
		t.Errorf("got %q %v", got, ok)
	}
	for _, s := range []string{"https://x/mcp?a=1", "https://x/mcp#f", "", "://x", "https://x/mcp?"} {
		if _, ok := NormalizeResource(s); ok {
			t.Errorf("deveria recusar %q", s)
		}
	}
	if got, ok := NormalizeResource("https://x:8443/mcp"); !ok || got != "https://x:8443/mcp" {
		t.Errorf("porta não padrão: %q %v", got, ok)
	}
}

func TestRandomTokenAndHash(t *testing.T) {
	a, err := RandomToken()
	if err != nil || len(a) != 43 {
		t.Fatalf("token %q err %v", a, err)
	}
	b, _ := RandomToken()
	if a == b {
		t.Error("tokens iguais")
	}
	if got := Hash("abc"); got != "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" {
		t.Errorf("hash %s", got)
	}
}
