// Package cloudflare mantém atualizada a lista de faixas de IP da Cloudflare.
package cloudflare

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/engviniciusbs/korego/internal/store"
)

const (
	settingKey      = "cloudflare.ranges"
	defaultV4URL    = "https://www.cloudflare.com/ips-v4"
	defaultV6URL    = "https://www.cloudflare.com/ips-v6"
	refreshInterval = 24 * time.Hour
	// retryAfterFailure é a espera após uma atualização que falhou.
	retryAfterFailure = time.Hour
	minV4Bits         = 8
	minV6Bits         = 20
	fetchTimeout      = 15 * time.Second
	maxBody           = 1 << 20
	minV4             = 10
	minV6             = 1
)

type list struct {
	V4 []string `json:"v4"`
	V6 []string `json:"v6"`
}

// Ranges guarda as faixas da Cloudflare: embutidas, ou a última lista boa
// baixada (persistida na configuração cloudflare.ranges).
type Ranges struct {
	// V4URL e V6URL são as fontes das listas (trocáveis em testes).
	V4URL, V6URL string

	// retryInterval é a espera após uma atualização que falhou.
	retryInterval time.Duration

	db     *store.DB
	client *http.Client

	mu  sync.RWMutex
	cur list
}

// New cria o Ranges. db pode ser nil (só a lista embutida); client nil usa
// http.DefaultClient. Uma lista persistida válida tem prioridade sobre a embutida.
func New(db *store.DB, client *http.Client) *Ranges {
	if client == nil {
		client = http.DefaultClient
	}
	r := &Ranges{
		V4URL: defaultV4URL, V6URL: defaultV6URL, db: db, client: client,
		retryInterval: retryAfterFailure,
		cur:           list{V4: defaultV4, V6: defaultV6},
	}
	if db != nil {
		if raw, ok, err := db.GetSetting(context.Background(), settingKey); err == nil && ok {
			var l list
			if json.Unmarshal([]byte(raw), &l) == nil {
				v4, v6 := valid(l.V4, true), valid(l.V6, false)
				if len(v4) >= minV4 && len(v6) >= minV6 {
					r.cur = list{V4: v4, V6: v6}
				}
			}
		}
	}
	return r
}

// Get devolve as faixas IPv4 seguidas das IPv6 (cópia).
func (r *Ranges) Get() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.cur.V4)+len(r.cur.V6))
	out = append(out, r.cur.V4...)
	return append(out, r.cur.V6...)
}

// Run atualiza agora e depois a cada 24 h (1 h após uma falha), até ctx terminar.
func (r *Ranges) Run(ctx context.Context) {
	for {
		wait := refreshInterval
		if err := r.Refresh(ctx); err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("cloudflare: atualizar faixas: %v", err)
			wait = r.retryInterval
		}
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		}
	}
}

// Refresh baixa as duas listas e, se ambas forem válidas, passa a usá-las e
// as persiste. Em qualquer falha a lista atual é mantida.
func (r *Ranges) Refresh(ctx context.Context) error {
	v4, err := r.fetch(ctx, r.V4URL, true, minV4)
	if err != nil {
		return err
	}
	v6, err := r.fetch(ctx, r.V6URL, false, minV6)
	if err != nil {
		return err
	}
	l := list{V4: v4, V6: v6}
	if r.db != nil {
		raw, _ := json.Marshal(l)
		if err := r.db.SetSetting(ctx, settingKey, string(raw)); err != nil {
			return err
		}
	}
	r.mu.Lock()
	r.cur = l
	r.mu.Unlock()
	return nil
}

func (r *Ranges) fetch(ctx context.Context, url string, v4 bool, min int) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: status %d", url, resp.StatusCode)
	}
	var lines []string
	sc := bufio.NewScanner(io.LimitReader(resp.Body, maxBody))
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	out := valid(lines, v4)
	if len(out) < min {
		return nil, fmt.Errorf("%s: lista com %d faixas válidas, mínimo %d", url, len(out), min)
	}
	return out, nil
}

// valid devolve os CIDRs válidos da família pedida, normalizados.
func valid(lines []string, v4 bool) []string {
	var out []string
	for _, ln := range lines {
		p, err := netip.ParsePrefix(strings.TrimSpace(ln))
		if err != nil || p.Addr().Is4() != v4 || p.Addr().Is4In6() {
			continue
		}
		// Recusa faixas absurdamente largas (ex.: 0.0.0.0/0 liberaria o mundo).
		if (v4 && p.Bits() < minV4Bits) || (!v4 && p.Bits() < minV6Bits) {
			continue
		}
		out = append(out, p.Masked().String())
	}
	return out
}
