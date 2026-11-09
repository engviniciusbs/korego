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
	h.start(w, r, "proxy.takeover", h.mig.StartTakeover)
}

// rollback serves POST /api/proxy/rollback: {code} → 202.
func (h migrationHandlers) rollback(w http.ResponseWriter, r *http.Request) {
	h.start(w, r, "proxy.rollback", h.mig.StartRollback)
}

type migrationStarted struct {
	Status string `json:"status"`
	Topic  string `json:"topic"`
}

func (h migrationHandlers) start(w http.ResponseWriter, r *http.Request, action string,
	startFn func(context.Context, func(migrate.Result)) error) {
	sess, ok := CurrentSession(r.Context())
	if !ok {
		WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Sessão inválida.")
		return
	}
	ip := auth.ClientIP(r)
	rec := func(meta map[string]any) {
		ctx, cancel := context.WithTimeout(context.Background(), auditTimeout)
		defer cancel()
		h.svc.Record(ctx, audit.Entry{UserID: &sess.UserID, Action: action, IP: ip, Metadata: meta})
	}
	if !h.confirmCode(w, r, sess.UserID, ip, rec) {
		return
	}
	ctx, cancel := detached(r, mutationTimeout)
	defer cancel()
	err := startFn(ctx, func(res migrate.Result) {
		rec(map[string]any{"ok": res.Outcome == migrate.OutcomeOK, "stage": "done",
			"result": res.Outcome, "message": res.Message})
	})
	if err != nil {
		var ref *migrate.Refusal
		if errors.As(err, &ref) {
			rec(map[string]any{"ok": false, "stage": "refused", "code": ref.Code})
		} else {
			rec(map[string]any{"ok": false, "stage": "refused"})
		}
		migrationError(w, err)
		return
	}
	rec(map[string]any{"ok": true, "stage": "started"})
	WriteJSON(w, http.StatusAccepted, migrationStarted{Status: "running", Topic: migrate.TopicMigration})
}

// confirmCode exige um código TOTP novo ou de recuperação no corpo ({code}).
// Sem código: 403 CODE_REQUIRED; errado: 403 INVALID_CODE (conta para o
// limite de tentativas do IP).
func (h migrationHandlers) confirmCode(w http.ResponseWriter, r *http.Request, userID int64, ip string,
	rec func(map[string]any)) bool {
	var in struct {
		Code string `json:"code"`
	}
	if r.ContentLength != 0 && !decodeJSON(w, r, &in) {
		return false
	}
	now := time.Now()
	if h.svc.Now != nil {
		now = h.svc.Now()
	}
	if h.svc.Limiter != nil && h.svc.Limiter.Blocked(ip, now) {
		rec(map[string]any{"ok": false, "stage": "rate_limited"})
		WriteError(w, http.StatusTooManyRequests, "RATE_LIMITED", "Muitas tentativas. Tente novamente mais tarde.")
		return false
	}
	if strings.TrimSpace(in.Code) == "" {
		WriteError(w, http.StatusForbidden, "CODE_REQUIRED", "Confirme com o código do autenticador ou um código de recuperação.")
		return false
	}
	ctx, cancel := detached(r, auditTimeout)
	defer cancel()
	ok, err := h.svc.VerifyCode(ctx, userID, in.Code)
	if err != nil {
		internalError(w, err)
		return false
	}
	if !ok {
		if h.svc.Limiter != nil {
			h.svc.Limiter.Fail(ip, now)
		}
		rec(map[string]any{"ok": false, "stage": "invalid_code"})
		WriteError(w, http.StatusForbidden, "INVALID_CODE", "Código inválido.")
		return false
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
