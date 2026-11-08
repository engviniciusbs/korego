package api

import (
	"errors"
	"io"
	"log"
	"net/http"
	"regexp"

	"github.com/engviniciusbs/korego/internal/audit"
	"github.com/engviniciusbs/korego/internal/auth"
	"github.com/engviniciusbs/korego/internal/deploy"
	"github.com/engviniciusbs/korego/internal/github"
)

const maxWebhookBody = 10 << 20

// signatureHeader é o formato exigido de X-Hub-Signature-256.
var signatureHeader = regexp.MustCompile(`^sha256=[0-9a-f]{64}$`)

type webhookHandlers struct {
	svc        *auth.Service
	gh         *github.Service
	deploys    *deploy.Service
	deliveries *deliverySet
}

// github serves POST /api/webhooks/github. É pública: a assinatura HMAC com o
// segredo do App autentica a entrega.
func (h webhookHandlers) github(w http.ResponseWriter, r *http.Request) {
	// Cabeçalho ausente ou fora do formato: recusa antes de ler o corpo,
	// consultar o banco ou decifrar o segredo.
	if !signatureHeader.MatchString(r.Header.Get("X-Hub-Signature-256")) {
		WriteError(w, http.StatusUnauthorized, "INVALID_SIGNATURE", "Assinatura do webhook inválida.")
		return
	}
	app, ok, err := h.gh.Connected(r.Context())
	if err != nil {
		internalError(w, err)
		return
	}
	if !ok {
		WriteError(w, http.StatusNotFound, "NOT_CONNECTED", "O GitHub não está conectado.")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxWebhookBody))
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			WriteError(w, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", "O corpo do webhook passa de 10 MiB.")
			return
		}
		WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "Corpo da requisição inválido.")
		return
	}
	secret, err := h.gh.Box.Decrypt(app.WebhookSecretEnc)
	if err != nil {
		internalError(w, errors.New("github: decifrar o segredo do webhook"))
		return
	}
	if !github.VerifySignature(secret, body, r.Header.Get("X-Hub-Signature-256")) {
		WriteError(w, http.StatusUnauthorized, "INVALID_SIGNATURE", "Assinatura do webhook inválida.")
		return
	}

	switch r.Header.Get("X-GitHub-Event") {
	case "push":
		h.push(w, r, body)
	default: // ping e outros eventos
		w.WriteHeader(http.StatusNoContent)
	}
}

func (h webhookHandlers) push(w http.ResponseWriter, r *http.Request, body []byte) {
	p, err := github.ParsePush(body)
	if errors.Is(err, github.ErrNotBranch) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err != nil {
		WriteError(w, http.StatusBadRequest, "INVALID_PAYLOAD", "Evento push inválido.")
		return
	}
	if p.Deleted {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	// Só depois da assinatura: entregas forjadas não entram no conjunto.
	delivery := r.Header.Get("X-GitHub-Delivery")
	if h.deliveries.seen(delivery) {
		WriteJSON(w, http.StatusAccepted, map[string]any{"queued": 0, "duplicate": true})
		return
	}
	ctx, cancel := detached(r, mutationTimeout)
	defer cancel()
	apps, err := h.gh.DB.AppsByRepo(ctx, p.Repo, p.Branch)
	if err != nil {
		h.deliveries.forget(delivery) // pode ser reenviada
		internalError(w, err)
		return
	}
	queued := 0
	for _, a := range apps {
		if _, err := h.deploys.Deploy(ctx, a.ID, "webhook", p.After, p.Message); err != nil {
			if errors.Is(err, deploy.ErrAppDeleting) {
				log.Printf("webhook: %s está sendo removida; push ignorado", a.Slug)
			} else {
				log.Printf("webhook: enfileirar deploy de %s: %v", a.Slug, err)
			}
			continue
		}
		queued++
		h.svc.Record(ctx, audit.Entry{UserID: nil, Action: "deploy.webhook", Target: a.Slug, IP: auth.ClientIP(r),
			Metadata: map[string]any{"repo": p.Repo, "branch": p.Branch, "sha": p.After, "delivery": delivery}})
	}
	WriteJSON(w, http.StatusAccepted, map[string]any{"queued": queued})
}
