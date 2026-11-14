package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/engviniciusbs/korego/internal/audit"
	"github.com/engviniciusbs/korego/internal/auth"
	"github.com/engviniciusbs/korego/internal/migrate"
)

type migrationHandlers struct {
	svc *auth.Service
	mig *migrate.Engine
}

type trialCheckBody struct {
	Sites []migrate.SiteCheck `json:"sites"`
}

// trialCheck serves GET /api/proxy/trial-check.
func (h migrationHandlers) trialCheck(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := detached(r, mutationTimeout)
	defer cancel()
	res, err := h.mig.TrialCheck(ctx)
	if err != nil {
		migrationError(w, err)
		return
	}
	WriteJSON(w, http.StatusOK, trialCheckBody{Sites: res})
}

// takeover serves POST /api/proxy/takeover: {code} → 202 e a troca em
// segundo plano (progresso no tópico proxy-migration).
func (h migrationHandlers) takeover(w http.ResponseWriter, r *http.Request) {
	h.start(w, r, "proxy.takeover", h.mig.PrecheckTakeover, h.mig.StartTakeover)
}

// rollback serves POST /api/proxy/rollback: {code} → 202.
func (h migrationHandlers) rollback(w http.ResponseWriter, r *http.Request) {
	h.start(w, r, "proxy.rollback", h.mig.PrecheckRollback, h.mig.StartRollback)
}

type migrationStarted struct {
	Status string `json:"status"`
	Topic  string `json:"topic"`
}

// start: recusas baratas (troca em andamento, estado do proxy) antes de
// conferir e gastar o código; depois o código; depois a troca.
func (h migrationHandlers) start(w http.ResponseWriter, r *http.Request, action string,
	precheck func(context.Context) error, startFn func(context.Context, func(migrate.Result)) error) {
	sess, ok := CurrentSession(r.Context())
	if !ok {
		WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Sessão inválida.")
		return
	}
	ip := auth.ClientIP(r)
	// Sem cancelamento, mas com os valores da requisição (o token da auditoria):
	// o resultado chega depois que a resposta já saiu.
	base := context.WithoutCancel(r.Context())
	rec := func(meta map[string]any) {
		ctx, cancel := context.WithTimeout(base, auditTimeout)
		defer cancel()
		h.svc.Record(ctx, audit.Entry{UserID: &sess.UserID, Action: action, IP: ip, Metadata: meta})
	}
	refused := func(err error) {
		var ref *migrate.Refusal
		if errors.As(err, &ref) {
			rec(map[string]any{"ok": false, "stage": "refused", "code": ref.Code})
		} else {
			rec(map[string]any{"ok": false, "stage": "refused"})
		}
		migrationError(w, err)
	}
	ctx, cancel := detached(r, mutationTimeout)
	defer cancel()
	if err := precheck(ctx); err != nil {
		refused(err)
		return
	}
	if !confirmCode(h.svc, w, r, sess.UserID, ip, rec) {
		return
	}
	err := startFn(ctx, func(res migrate.Result) {
		rec(map[string]any{"ok": res.Outcome == migrate.OutcomeOK, "stage": "done",
			"result": res.Outcome, "message": res.Message})
	})
	if err != nil {
		refused(err)
		return
	}
	rec(map[string]any{"ok": true, "stage": "started"})
	WriteJSON(w, http.StatusAccepted, migrationStarted{Status: "running", Topic: migrate.TopicMigration})
}

// confirmCode exige um código TOTP novo ou de recuperação no corpo ({code}).
// Como no login, toda tentativa passa por Limiter.Attempt (bloqueado: 429
// RATE_LIMITED) e o acerto zera o contador. Sem código: 403 CODE_REQUIRED;
// errado: 403 INVALID_CODE. Todas as recusas são auditadas. Se o gate de
// token (requireCodeForToken) já conferiu o código do cabeçalho, devolve true
// sem ler o corpo.
func confirmCode(svc *auth.Service, w http.ResponseWriter, r *http.Request, userID int64, ip string,
	rec func(map[string]any)) bool {
	if codeConfirmed(r.Context()) {
		return true
	}
	var in struct {
		Code string `json:"code"`
	}
	if r.ContentLength != 0 && !decodeJSON(w, r, &in) {
		return false
	}
	now := time.Now()
	if svc.Now != nil {
		now = svc.Now()
	}
	if svc.Limiter != nil && !svc.Limiter.Attempt(ip, now) {
		rec(map[string]any{"ok": false, "stage": "rate_limited"})
		WriteError(w, http.StatusTooManyRequests, "RATE_LIMITED", msgRateLimited)
		return false
	}
	if strings.TrimSpace(in.Code) == "" {
		rec(map[string]any{"ok": false, "stage": "code_required"})
		WriteError(w, http.StatusForbidden, "CODE_REQUIRED", msgCodeRequired)
		return false
	}
	ctx, cancel := detached(r, auditTimeout)
	defer cancel()
	ok, err := svc.VerifyCode(ctx, userID, in.Code)
	if err != nil {
		internalError(w, err)
		return false
	}
	if !ok {
		rec(map[string]any{"ok": false, "stage": "invalid_code"})
		WriteError(w, http.StatusForbidden, "INVALID_CODE", msgInvalidCode)
		return false
	}
	if svc.Limiter != nil {
		svc.Limiter.Reset(ip)
	}
	return true
}

// migrationError traduz as recusas do motor.
func migrationError(w http.ResponseWriter, err error) {
	var ref *migrate.Refusal
	if errors.As(err, &ref) {
		WriteError(w, http.StatusConflict, ref.Code, ref.Message)
		return
	}
	internalError(w, err)
}
