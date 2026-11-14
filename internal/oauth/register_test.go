package oauth

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSanitizeRegistration(t *testing.T) {
	t.Run("campos extras e grant_types filtrados", func(t *testing.T) {
		r, err := SanitizeRegistration(json.RawMessage(`{"client_name":"Meu","redirect_uris":["http://127.0.0.1:1/cb"],
			"scope":"x","logo_uri":"http://e/l.png","software_id":"s",
			"grant_types":["authorization_code","refresh_token","urn:ietf:params:oauth:grant-type:device_code"]}`))
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(r.GrantTypes, []string{"authorization_code", "refresh_token"}) {
			t.Errorf("grants %v", r.GrantTypes)
		}
		if r.ClientName != "Meu" {
			t.Errorf("nome %q", r.ClientName)
		}
	})
	t.Run("nome padrão", func(t *testing.T) {
		r, err := SanitizeRegistration(json.RawMessage(`{"redirect_uris":["http://localhost/cb"]}`))
		if err != nil || r.ClientName != DefaultClientName || DefaultClientName != "Agente sem nome" {
			t.Errorf("%v %q", err, r.ClientName)
		}
		if !reflect.DeepEqual(r.GrantTypes, []string{"authorization_code", "refresh_token"}) {
			t.Errorf("grants padrão %v", r.GrantTypes)
		}
	})
	t.Run("nome com controles e longo", func(t *testing.T) {
		name := "a\x00b\nc" + strings.Repeat("é", 80)
		body, _ := json.Marshal(map[string]any{"client_name": name, "redirect_uris": []string{"http://localhost/cb"}})
		r, err := SanitizeRegistration(body)
		if err != nil {
			t.Fatal(err)
		}
		if utf8.RuneCountInString(r.ClientName) != 64 {
			t.Errorf("runas %d", utf8.RuneCountInString(r.ClientName))
		}
		for _, c := range r.ClientName {
			if c < 0x20 || c == 0x7f {
				t.Errorf("controle %q", c)
			}
		}
	})
	t.Run("nome só de controles vira padrão", func(t *testing.T) {
		r, err := SanitizeRegistration(json.RawMessage(`{"client_name":"\u0000\n","redirect_uris":["http://localhost/cb"]}`))
		if err != nil || r.ClientName != DefaultClientName {
			t.Errorf("%v %q", err, r.ClientName)
		}
	})
	t.Run("redirect_uris inválidos", func(t *testing.T) {
		six := `["http://localhost:1/a","http://localhost:2/a","http://localhost:3/a","http://localhost:4/a","http://localhost:5/a","http://localhost:6/a"]`
		for _, body := range []string{
			`{"redirect_uris":` + six + `}`,
			`{"redirect_uris":[]}`,
			`{}`,
		} {
			if _, err := SanitizeRegistration(json.RawMessage(body)); !errors.Is(err, ErrInvalidRedirect) {
				t.Errorf("%s: %v", body, err)
			}
		}
		_, err := SanitizeRegistration(json.RawMessage(`{"redirect_uris":["http://localhost/ok","vscode://x"]}`))
		var re RedirectError
		if !errors.Is(err, ErrInvalidRedirect) || !errors.As(err, &re) || re.URI != "vscode://x" {
			t.Errorf("err %v", err)
		}
	})
	t.Run("json inválido", func(t *testing.T) {
		for _, body := range []string{`{`, ``, `[]`, `{"redirect_uris":"x"}`} {
			if _, err := SanitizeRegistration(json.RawMessage(body)); !errors.Is(err, ErrInvalidMetadata) {
				t.Errorf("%q: %v", body, err)
			}
		}
	})
}
