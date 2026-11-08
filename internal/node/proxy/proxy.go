// Package proxy controla o proxy reverso do KoreGO (Caddy).
//
// Este arquivo define as rotas que o pacote recebe. caddyconfig.go
// transforma as rotas na configuração JSON nativa do Caddy e admin.go
// conversa com a Admin API (127.0.0.1:2019) para aplicá-la.
//
// O pacote não importa outros pacotes internos (exceto node/docker).
package proxy

// Valores aceitos em Route.WWWRedirect.
const (
	WWWNone      = "none"
	WWWToApex    = "www_to_apex"
	WWWApexToWWW = "apex_to_www"
)

// Route é um hostname publicado pelo proxy.
type Route struct {
	Host        string // hostname principal
	Upstream    string // "127.0.0.1:20001"
	ForceHTTPS  bool
	WWWRedirect string // none | www_to_apex | apex_to_www ("" = none)
}
