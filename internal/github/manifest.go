package github

import (
	"encoding/json"
	"strings"
)

// Manifest gera o manifesto (JSON determinístico) do GitHub App para o painel
// em panelURL; hostname compõe o nome do App.
func Manifest(panelURL, hostname string) []byte {
	panelURL = strings.TrimRight(panelURL, "/")
	// Structs mantêm a ordem dos campos, o que torna a saída determinística.
	type hook struct {
		URL    string `json:"url"`
		Active bool   `json:"active"`
	}
	type perms struct {
		Contents string `json:"contents"`
		Metadata string `json:"metadata"`
	}
	m := struct {
		Name               string   `json:"name"`
		URL                string   `json:"url"`
		HookAttributes     hook     `json:"hook_attributes"`
		RedirectURL        string   `json:"redirect_url"`
		Public             bool     `json:"public"`
		DefaultPermissions perms    `json:"default_permissions"`
		DefaultEvents      []string `json:"default_events"`
	}{
		Name:               appName(hostname),
		URL:                panelURL,
		HookAttributes:     hook{URL: panelURL + "/api/webhooks/github", Active: true},
		RedirectURL:        panelURL + "/api/github/callback",
		DefaultPermissions: perms{Contents: "read", Metadata: "read"},
		DefaultEvents:      []string{"push"},
	}
	b, _ := json.Marshal(m)
	return b
}

func appName(hostname string) string {
	var sb strings.Builder
	for _, r := range hostname {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' {
			sb.WriteRune(r)
		} else {
			sb.WriteByte('-')
		}
	}
	name := "KoreGO-" + sb.String()
	if len(name) > 34 {
		name = name[:34]
	}
	return name
}
