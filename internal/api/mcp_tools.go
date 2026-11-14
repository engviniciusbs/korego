package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/engviniciusbs/korego/internal/domains"
)

const (
	containerLogsDefault = 200
	containerLogsMax     = 1000

	deployWaitDefault = 60 * time.Second
	deployWaitMax     = 120 * time.Second
	deployWaitPoll    = 2 * time.Second
	deployWaitLines   = 50
	// deployLogPages limita as páginas lidas pelo deploy_wait (cada uma até maxLogEntries).
	deployLogPages = 100
)

// restArgs é o que uma entrada de ferramenta vira na requisição REST.
type restArgs struct {
	id    string // substitui {id} no caminho
	query url.Values
	body  any // nil = sem corpo
	code  string
}

type restInput interface{ rest() restArgs }

// Entradas. Só "body" vai no corpo (decodeJSON recusa campos desconhecidos).

type noInput struct{}

func (noInput) rest() restArgs { return restArgs{} }

type idInput struct {
	ID int64 `json:"id" jsonschema:"ID do recurso"`
}

func (in idInput) rest() restArgs { return restArgs{id: itoa64(in.ID)} }

type gatedIDInput struct {
	ID   int64  `json:"id" jsonschema:"ID do recurso"`
	Code string `json:"code,omitempty" jsonschema:"Obrigatório: código de 6 dígitos do autenticador (ou um código de recuperação) que o usuário acabou de digitar. Nunca invente nem reutilize."`
}

func (in gatedIDInput) rest() restArgs { return restArgs{id: itoa64(in.ID), code: in.Code} }

type containerInput struct {
	ID string `json:"id" jsonschema:"ID do container (12 ou 64 caracteres hexadecimais)"`
}

func (in containerInput) rest() restArgs { return restArgs{id: in.ID} }

type gatedContainerInput struct {
	ID   string `json:"id" jsonschema:"ID do container (12 ou 64 caracteres hexadecimais)"`
	Code string `json:"code,omitempty" jsonschema:"Obrigatório: código de 6 dígitos do autenticador (ou um código de recuperação) que o usuário acabou de digitar. Nunca invente nem reutilize."`
}

func (in gatedContainerInput) rest() restArgs { return restArgs{id: in.ID, code: in.Code} }

type codeInput struct {
	Code string `json:"code,omitempty" jsonschema:"Obrigatório: código de 6 dígitos do autenticador (ou um código de recuperação) que o usuário acabou de digitar. Nunca invente nem reutilize."`
}

func (in codeInput) rest() restArgs { return restArgs{code: in.Code} }

type bodyInput[T any] struct {
	Body T `json:"body" jsonschema:"Corpo da requisição, igual ao da API REST"`
}

func (in bodyInput[T]) rest() restArgs { return restArgs{body: in.Body} }

type idBodyInput[T any] struct {
	ID   int64 `json:"id" jsonschema:"ID do recurso"`
	Body T     `json:"body" jsonschema:"Corpo da requisição, igual ao da API REST"`
}

func (in idBodyInput[T]) rest() restArgs { return restArgs{id: itoa64(in.ID), body: in.Body} }

type gatedBodyInput[T any] struct {
	Code string `json:"code,omitempty" jsonschema:"Obrigatório: código de 6 dígitos do autenticador (ou um código de recuperação) que o usuário acabou de digitar. Nunca invente nem reutilize."`
	Body T      `json:"body" jsonschema:"Corpo da requisição, igual ao da API REST"`
}

func (in gatedBodyInput[T]) rest() restArgs { return restArgs{body: in.Body, code: in.Code} }

type rangeInput struct {
	Range string `json:"range" jsonschema:"Intervalo: 1h, 24h ou 7d"`
}

func (in rangeInput) rest() restArgs { return restArgs{query: url.Values{"range": {in.Range}}} }

type usageHistoryInput struct {
	Owner string `json:"owner" jsonschema:"Chave do dono, como em usage_current"`
	Range string `json:"range" jsonschema:"Intervalo: 1h, 24h ou 7d"`
}

func (in usageHistoryInput) rest() restArgs {
	return restArgs{query: url.Values{"owner": {in.Owner}, "range": {in.Range}}}
}

type usageAtInput struct {
	T      int64  `json:"t" jsonschema:"Instante em segundos unix"`
	Range  string `json:"range" jsonschema:"Intervalo do gráfico de onde veio o instante: 1h, 24h ou 7d"`
	Metric string `json:"metric,omitempty" jsonschema:"cpu (padrão) ou mem"`
}

func (in usageAtInput) rest() restArgs {
	q := url.Values{"t": {itoa64(in.T)}, "range": {in.Range}}
	setQuery(q, "metric", in.Metric)
	return restArgs{query: q}
}

type auditInput struct {
	Limit  int64 `json:"limit,omitempty" jsonschema:"Quantas entradas (padrão da API)"`
	Before int64 `json:"before,omitempty" jsonschema:"Só entradas com ID menor que este (paginação: nextBefore)"`
}

func (in auditInput) rest() restArgs {
	q := url.Values{}
	setQueryInt(q, "limit", in.Limit)
	setQueryInt(q, "before", in.Before)
	return restArgs{query: q}
}

type containerLogsInput struct {
	ID   string `json:"id" jsonschema:"ID do container (12 ou 64 caracteres hexadecimais)"`
	Tail int64  `json:"tail,omitempty" jsonschema:"Últimas N linhas (padrão 200, máximo 1000)"`
}

func (in containerLogsInput) rest() restArgs {
	tail := in.Tail
	if tail <= 0 {
		tail = containerLogsDefault
	}
	tail = min(tail, containerLogsMax)
	return restArgs{id: in.ID, query: url.Values{"tail": {itoa64(tail)}}}
}

type appListInput struct {
	Project int64 `json:"project,omitempty" jsonschema:"Só as apps deste projeto"`
}

func (in appListInput) rest() restArgs {
	q := url.Values{}
	setQueryInt(q, "project", in.Project)
	return restArgs{query: q}
}

type deployListInput struct {
	ID    int64 `json:"id" jsonschema:"ID da aplicação"`
	Limit int64 `json:"limit,omitempty" jsonschema:"Quantos deploys (1 a 100, padrão 20)"`
}

func (in deployListInput) rest() restArgs {
	q := url.Values{}
	setQueryInt(q, "limit", in.Limit)
	return restArgs{id: itoa64(in.ID), query: q}
}

type deployLogInput struct {
	ID   int64 `json:"id" jsonschema:"ID do deploy"`
	From int64 `json:"from,omitempty" jsonschema:"Devolve as linhas depois desta (paginação pelo campo n)"`
}

func (in deployLogInput) rest() restArgs {
	q := url.Values{}
	setQueryInt(q, "from", in.From)
	return restArgs{id: itoa64(in.ID), query: q}
}

type deployWaitInput struct {
	ID             int64 `json:"id" jsonschema:"ID do deploy"`
	TimeoutSeconds int64 `json:"timeoutSeconds,omitempty" jsonschema:"Espera máxima em segundos (padrão 60, de 1 a 120)"`
}

type deployWaitOutput struct {
	Status    string   `json:"status"`
	TimedOut  bool     `json:"timedOut"`
	LastLines []string `json:"lastLines"`
}

func itoa64(n int64) string { return strconv.FormatInt(n, 10) }

func setQuery(q url.Values, k, v string) {
	if v != "" {
		q.Set(k, v)
	}
}

func setQueryInt(q url.Values, k string, v int64) {
	if v != 0 {
		q.Set(k, itoa64(v))
	}
}

// restTool liga uma ferramenta a uma rota do núcleo (path com /api).
type restTool struct {
	name, desc   string
	method, path string
	idDesc       string // descrição do "id" nesta ferramenta
	gated        bool
}

type toolBox struct {
	s *mcp.Server
	d dispatcher
}

func (tb toolBox) has(method, path string) bool { return routeExists(tb.d.core, method, path) }

func (tb toolBox) tool(t restTool, schema any) *mcp.Tool {
	tool := &mcp.Tool{Name: t.name, Description: t.desc, InputSchema: schema, Annotations: &mcp.ToolAnnotations{}}
	switch {
	case t.method == http.MethodGet:
		tool.Annotations.ReadOnlyHint = true
	case t.gated:
		yes := true
		tool.Annotations.DestructiveHint = &yes
	}
	return tool
}

// addREST registra t só se a rota existe no núcleo.
func addREST[In restInput](tb toolBox, t restTool) {
	if !tb.has(t.method, t.path) {
		return
	}
	schema := inputSchema[In]()
	if p := schema.Properties["id"]; p != nil && t.idDesc != "" {
		p.Description = t.idDesc
	}
	mcp.AddTool(tb.s, tb.tool(t, schema), func(ctx context.Context, req *mcp.CallToolRequest, in In) (*mcp.CallToolResult, any, error) {
		ctx, cancel := boundToRequest(ctx)
		defer cancel()
		id, ok := identityOf(req)
		if !ok {
			return noIdentityResult(), nil, nil
		}
		a := in.rest()
		path := strings.ReplaceAll(t.path, "{id}", url.PathEscape(a.id))
		return toolResult(tb.d.do(ctx, id, t.method, path, a.query, a.body, a.code)), nil, nil
	})
}

// registerMCPTools registra a tabela de ferramentas.
func registerMCPTools(s *mcp.Server, d dispatcher) {
	tb := toolBox{s: s, d: d}
	const (
		get, post, put, del     = http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete
		appIDDesc, deployIDDesc = "ID da aplicação", "ID do deploy"
	)

	// Visão geral.
	addREST[noInput](tb, restTool{name: "system_info", method: get, path: "/api/system",
		desc: "Versão do KoreGO, host, uptime e se Docker e PM2 estão disponíveis."})
	addREST[noInput](tb, restTool{name: "metrics_current", method: get, path: "/api/metrics/current",
		desc: "Última amostra de CPU, memória, disco e rede do servidor."})
	addREST[rangeInput](tb, restTool{name: "metrics_history", method: get, path: "/api/metrics/history",
		desc: "Histórico das métricas do servidor no intervalo (1h, 24h ou 7d)."})
	addREST[noInput](tb, restTool{name: "usage_current", method: get, path: "/api/usage",
		desc: "Consumo atual de CPU e memória por dono (app, container, processo)."})
	addREST[usageHistoryInput](tb, restTool{name: "usage_history", method: get, path: "/api/usage/history",
		desc: "Histórico de consumo de um dono no intervalo (1h, 24h ou 7d)."})
	addREST[usageAtInput](tb, restTool{name: "usage_at", method: get, path: "/api/usage/at",
		desc: "Quem mais consumia CPU ou memória num instante do histórico."})
	addREST[noInput](tb, restTool{name: "pm2_list", method: get, path: "/api/pm2",
		desc: "Processos do PM2 no servidor."})
	addREST[auditInput](tb, restTool{name: "audit_list", method: get, path: "/api/audit",
		desc: "Registro de auditoria, do mais novo ao mais antigo (pagine com before = nextBefore)."})

	// Containers.
	addREST[noInput](tb, restTool{name: "container_list", method: get, path: "/api/containers",
		desc: "Lista os containers Docker do servidor."})
	addREST[containerInput](tb, restTool{name: "container_get", method: get, path: "/api/containers/{id}",
		desc: "Detalhes de um container."})
	addREST[containerLogsInput](tb, restTool{name: "container_logs", method: get, path: "/api/containers/{id}/logs",
		desc: "Últimas linhas do log de um container (retrato, sem acompanhar)."})
	addREST[containerInput](tb, restTool{name: "container_start", method: post, path: "/api/containers/{id}/start",
		desc: "Inicia um container parado."})
	addREST[containerInput](tb, restTool{name: "container_stop", method: post, path: "/api/containers/{id}/stop",
		desc: "Para um container."})
	addREST[containerInput](tb, restTool{name: "container_restart", method: post, path: "/api/containers/{id}/restart",
		desc: "Reinicia um container."})
	addREST[gatedContainerInput](tb, restTool{name: "container_remove", method: del, path: "/api/containers/{id}",
		gated: true, desc: "Remove um container (irreversível; exige code do autenticador)."})

	// GitHub.
	addREST[noInput](tb, restTool{name: "github_status", method: get, path: "/api/github",
		desc: "Se o GitHub App está conectado. Conectar se faz no painel, em Configurações → GitHub."})
	addREST[noInput](tb, restTool{name: "github_repos", method: get, path: "/api/github/repos",
		desc: "Repositórios que o GitHub App enxerga, com o branch padrão."})
	addREST[codeInput](tb, restTool{name: "github_disconnect", method: del, path: "/api/github",
		gated: true, desc: "Desconecta o GitHub App; todo auto-deploy para (exige code do autenticador)."})

	// Projetos e apps.
	addREST[noInput](tb, restTool{name: "project_list", method: get, path: "/api/projects",
		desc: "Lista os projetos."})
	addREST[bodyInput[projectInput]](tb, restTool{name: "project_create", method: post, path: "/api/projects",
		desc: "Cria um projeto (body: name, description)."})
	addREST[idBodyInput[projectInput]](tb, restTool{name: "project_update", method: put, path: "/api/projects/{id}",
		idDesc: "ID do projeto", desc: "Renomeia ou descreve um projeto."})
	addREST[gatedIDInput](tb, restTool{name: "project_delete", method: del, path: "/api/projects/{id}",
		idDesc: "ID do projeto", gated: true, desc: "Apaga um projeto vazio (irreversível; exige code do autenticador)."})
	addREST[appListInput](tb, restTool{name: "app_list", method: get, path: "/api/apps",
		desc: "Lista as aplicações, com o deploy atual e os domínios."})
	addREST[idInput](tb, restTool{name: "app_get", method: get, path: "/api/apps/{id}", idDesc: appIDDesc,
		desc: "Detalhes de uma aplicação (nunca inclui os valores das variáveis)."})
	addREST[bodyInput[AppInput]](tb, restTool{name: "app_create", method: post, path: "/api/apps",
		desc: "Cria uma aplicação. sourceType: github (repo dono/nome), git (URL https) ou image; buildType: dockerfile, compose ou image."})
	addREST[idBodyInput[AppInput]](tb, restTool{name: "app_update", method: put, path: "/api/apps/{id}", idDesc: appIDDesc,
		desc: "Altera uma aplicação (o slug não muda). Vale no próximo deploy."})
	addREST[gatedIDInput](tb, restTool{name: "app_delete", method: del, path: "/api/apps/{id}", idDesc: appIDDesc,
		gated: true, desc: "Apaga a aplicação, seus containers, imagens e arquivos (irreversível; exige code do autenticador)."})
	addREST[idInput](tb, restTool{name: "app_env_names", method: get, path: "/api/apps/{id}/env", idDesc: appIDDesc,
		desc: "Nomes das variáveis de ambiente da aplicação (os valores nunca voltam)."})
	addREST[idBodyInput[envInput]](tb, restTool{name: "app_env_set", method: put, path: "/api/apps/{id}/env", idDesc: appIDDesc,
		desc: "Grava (set: nome → valor) e remove (remove: nomes) variáveis. Só escrita; vale no próximo deploy."})

	// Deploys.
	addREST[idInput](tb, restTool{name: "deploy_start", method: post, path: "/api/apps/{id}/deploys", idDesc: appIDDesc,
		desc: "Dispara um deploy da aplicação. Acompanhe com deploy_wait."})
	addREST[deployListInput](tb, restTool{name: "deploy_list", method: get, path: "/api/apps/{id}/deploys",
		desc: "Deploys da aplicação, do mais novo ao mais antigo."})
	addREST[idInput](tb, restTool{name: "deploy_get", method: get, path: "/api/deploys/{id}", idDesc: deployIDDesc,
		desc: "Estado de um deploy."})
	addREST[deployLogInput](tb, restTool{name: "deploy_log", method: get, path: "/api/deploys/{id}/log",
		desc: "Linhas do log de um deploy a partir de from; done diz se o log terminou."})
	addREST[idInput](tb, restTool{name: "deploy_cancel", method: post, path: "/api/deploys/{id}/cancel", idDesc: deployIDDesc,
		desc: "Cancela um deploy em andamento."})
	addREST[idBodyInput[rollbackInput]](tb, restTool{name: "deploy_rollback", method: post, path: "/api/apps/{id}/rollback",
		idDesc: appIDDesc, desc: "Volta a aplicação para a imagem de um deploy anterior (body.deployId)."})
	addDeployWait(tb)

	// Domínios e proxy.
	addREST[noInput](tb, restTool{name: "domain_list", method: get, path: "/api/domains",
		desc: "Lista os domínios, com rotas, TLS e o estado do certificado."})
	addREST[bodyInput[domains.DomainInput]](tb, restTool{name: "domain_create", method: post, path: "/api/domains",
		desc: "Cria um domínio. routes: [{path \"/\", targetType app|address|static, appId…}]; tls.mode auto exige DNS apontando para o servidor."})
	addREST[idBodyInput[domains.DomainInput]](tb, restTool{name: "domain_update", method: put, path: "/api/domains/{id}",
		idDesc: "ID do domínio", desc: "Altera um domínio; campos ausentes ficam como estão (PUT parcial, como na API)."})
	addREST[idInput](tb, restTool{name: "domain_dns", method: get, path: "/api/domains/{id}/dns", idDesc: "ID do domínio",
		desc: "Confere se o DNS do domínio aponta para este servidor."})
	addREST[gatedIDInput](tb, restTool{name: "domain_delete", method: del, path: "/api/domains/{id}", idDesc: "ID do domínio",
		gated: true, desc: "Apaga um domínio e tira o site do ar (irreversível; exige code do autenticador)."})
	addProxyStatus(tb)
	addREST[noInput](tb, restTool{name: "proxy_retry", method: post, path: "/api/proxy/retry",
		desc: "Roda já uma reconciliação do proxy (Caddy) com os domínios."})
	addREST[noInput](tb, restTool{name: "proxy_trial_check", method: get, path: "/api/proxy/trial-check",
		desc: "Testa os sites no proxy em modo de teste, antes de assumir as portas 80/443."})
	addREST[codeInput](tb, restTool{name: "proxy_takeover", method: post, path: "/api/proxy/takeover",
		gated: true, desc: "Faz o KoreGO assumir as portas 80/443 do servidor web atual (exige code do autenticador)."})
	addREST[codeInput](tb, restTool{name: "proxy_rollback", method: post, path: "/api/proxy/rollback",
		gated: true, desc: "Devolve as portas 80/443 ao servidor web anterior (exige code do autenticador)."})

	// Certificados.
	addREST[noInput](tb, restTool{name: "certificate_list", method: get, path: "/api/certificates",
		desc: "Lista os certificados próprios (a chave privada nunca volta)."})
	addREST[bodyInput[certInput]](tb, restTool{name: "certificate_create", method: post, path: "/api/certificates",
		desc: "Adiciona um certificado próprio: body.name e body.pem (certificado + chave) ou body.path no servidor."})
	addREST[gatedIDInput](tb, restTool{name: "certificate_delete", method: del, path: "/api/certificates/{id}",
		idDesc: "ID do certificado", gated: true, desc: "Apaga um certificado (irreversível; exige code do autenticador)."})

	// Painel e atualização.
	addREST[noInput](tb, restTool{name: "panel_settings", method: get, path: "/api/settings/panel",
		desc: "Domínio e porta pública do painel."})
	addREST[gatedBodyInput[panelDomainInput]](tb, restTool{name: "panel_set_domain", method: put, path: "/api/settings/panel/domain",
		gated: true, desc: "Muda o domínio do painel; pode tirar o acesso do usuário e do agente (exige code do autenticador)."})
	addREST[gatedBodyInput[panelPortInput]](tb, restTool{name: "panel_set_public_port", method: put, path: "/api/settings/panel/public-port",
		gated: true, desc: "Liga ou desliga a porta pública do painel; pode tirar o acesso (exige code do autenticador)."})
	addREST[noInput](tb, restTool{name: "update_status", method: get, path: "/api/update",
		desc: "Versão atual e se há atualização do KoreGO."})
	addREST[codeInput](tb, restTool{name: "update_apply", method: post, path: "/api/update",
		gated: true, desc: "Aplica a atualização do KoreGO e reinicia o servidor (exige code do autenticador)."})
}

// addDeployWait: consulta o deploy a cada 2 s até um estado final ou o tempo
// (padrão 60 s, de 1 a 120 s) e devolve as últimas 50 linhas do log.
func addDeployWait(tb toolBox) {
	if !tb.has(http.MethodGet, "/api/deploys/{id}") || !tb.has(http.MethodGet, "/api/deploys/{id}/log") {
		return
	}
	t := restTool{name: "deploy_wait", method: http.MethodGet,
		desc: "Espera o deploy terminar (até timeoutSeconds, máximo 120) e devolve status, timedOut e as últimas 50 linhas do log. Se timedOut, chame de novo."}
	mcp.AddTool(tb.s, tb.tool(t, inputSchema[deployWaitInput]()),
		func(ctx context.Context, req *mcp.CallToolRequest, in deployWaitInput) (*mcp.CallToolResult, any, error) {
			ctx, cancel := boundToRequest(ctx)
			defer cancel()
			id, ok := identityOf(req)
			if !ok {
				return noIdentityResult(), nil, nil
			}
			wait := deployWaitDefault
			if in.TimeoutSeconds != 0 {
				wait = time.Duration(min(max(in.TimeoutSeconds, 1), int64(deployWaitMax/time.Second))) * time.Second
			}
			path := "/api/deploys/" + itoa64(in.ID)
			out := deployWaitOutput{LastLines: []string{}}
			for elapsed := time.Duration(0); ; {
				st, b := tb.d.do(ctx, id, http.MethodGet, path, nil, nil, "")
				if st != http.StatusOK {
					return toolResult(st, b), nil, nil
				}
				var resp struct {
					Deploy struct {
						Status string `json:"status"`
					} `json:"deploy"`
				}
				if err := json.Unmarshal(b, &resp); err != nil {
					return nil, nil, err
				}
				out.Status = resp.Deploy.Status
				if terminalStatus(out.Status) {
					break
				}
				if elapsed >= wait {
					out.TimedOut = true
					break
				}
				step := min(deployWaitPoll, wait-elapsed)
				if err := deployWaitSleep(ctx, step); err != nil {
					return nil, nil, err
				}
				elapsed += step
			}
			lines, res := lastDeployLines(ctx, tb.d, id, path+"/log")
			if res != nil {
				return res, nil, nil
			}
			out.LastLines = lines
			return valueResult(out), nil, nil
		})
}

// lastDeployLines pagina GET /deploys/{id}/log e guarda as últimas linhas.
func lastDeployLines(ctx context.Context, d dispatcher, id tokenIdentity, path string) ([]string, *mcp.CallToolResult) {
	lines := []string{}
	from := 0
	for range deployLogPages {
		st, b := d.do(ctx, id, http.MethodGet, path, url.Values{"from": {strconv.Itoa(from)}}, nil, "")
		if st != http.StatusOK {
			return nil, toolResult(st, b)
		}
		var resp struct {
			Entries []struct {
				N    int    `json:"n"`
				Text string `json:"text"`
			} `json:"entries"`
		}
		if err := json.Unmarshal(b, &resp); err != nil {
			return nil, errorResult("INTERNAL_ERROR (HTTP 500): " + err.Error())
		}
		for _, e := range resp.Entries {
			lines = append(lines, e.Text)
		}
		if len(lines) > deployWaitLines {
			lines = append([]string(nil), lines[len(lines)-deployWaitLines:]...)
		}
		if len(resp.Entries) < maxLogEntries {
			break
		}
		from = resp.Entries[len(resp.Entries)-1].N
	}
	return lines, nil
}

// addProxyStatus: o estado do proxy mais, por domínio, o certificado servido.
func addProxyStatus(tb toolBox) {
	if !tb.has(http.MethodGet, "/api/proxy") || !tb.has(http.MethodGet, "/api/domains") {
		return
	}
	t := restTool{name: "proxy_status", method: http.MethodGet,
		desc: "Estado do proxy (Caddy) e, por domínio, o certificado servido: é assim que se confirma que o HTTPS respondeu."}
	mcp.AddTool(tb.s, tb.tool(t, inputSchema[noInput]()),
		func(ctx context.Context, req *mcp.CallToolRequest, _ noInput) (*mcp.CallToolResult, any, error) {
			ctx, cancel := boundToRequest(ctx)
			defer cancel()
			id, ok := identityOf(req)
			if !ok {
				return noIdentityResult(), nil, nil
			}
			st, px := tb.d.do(ctx, id, http.MethodGet, "/api/proxy", nil, nil, "")
			if st != http.StatusOK {
				return toolResult(st, px), nil, nil
			}
			st, b := tb.d.do(ctx, id, http.MethodGet, "/api/domains", nil, nil, "")
			if st != http.StatusOK {
				return toolResult(st, b), nil, nil
			}
			type domainCert struct {
				ID       int64           `json:"id"`
				Hostname string          `json:"hostname"`
				TLS      json.RawMessage `json:"tls"`
				Cert     json.RawMessage `json:"cert"`
			}
			var list struct {
				Domains []domainCert `json:"domains"`
			}
			if err := json.Unmarshal(b, &list); err != nil {
				return nil, nil, err
			}
			if list.Domains == nil {
				list.Domains = []domainCert{}
			}
			return valueResult(map[string]any{"proxy": json.RawMessage(px), "domains": list.Domains}), nil, nil
		})
}
