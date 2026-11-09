package api

import (
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/engviniciusbs/korego/internal/store"
	"github.com/engviniciusbs/korego/internal/usage"
)

const (
	usageAtLimit     = 10
	maxOwnerKeyLen   = 200
	usageFutureSlack = time.Minute
)

// LatestUsage fornece o último retrato de consumo por dono (implementada por usage.Collector).
type LatestUsage interface {
	Latest() (usage.Snapshot, bool)
}

var ownerKeyRe = regexp.MustCompile(`^(project|container|pm2|service):.+$|^(session|kernel|other|rest)$`)

type usageHandlers struct {
	latest LatestUsage
	db     *store.DB
	now    func() time.Time
}

type usageProcDTO struct {
	PID        int     `json:"pid"`
	Name       string  `json:"name"`
	Cmd        string  `json:"cmd"`
	User       string  `json:"user"`
	CPUPercent float64 `json:"cpuPercent"`
	MemBytes   uint64  `json:"memBytes"`
}

type usageOwnerDTO struct {
	Key        string         `json:"key"`
	Kind       string         `json:"kind"`
	Label      string         `json:"label"`
	Managed    *bool          `json:"managed,omitempty"`
	CPUPercent float64        `json:"cpuPercent"`
	MemBytes   uint64         `json:"memBytes"`
	ProcCount  int            `json:"procCount"`
	Truncated  bool           `json:"truncated"`
	Processes  []usageProcDTO `json:"processes"`
}

type usageResponse struct {
	Time     time.Time       `json:"time"`
	DockerOK bool            `json:"dockerOk"`
	Owners   []usageOwnerDTO `json:"owners"`
}

type usageAtOwnerDTO struct {
	Key        string  `json:"key"`
	Kind       string  `json:"kind"`
	Label      string  `json:"label"`
	CPUPercent float64 `json:"cpuPercent"`
	MemBytes   uint64  `json:"memBytes"`
}

type usageAtResponse struct {
	T           int64             `json:"t"`
	StepSeconds int               `json:"stepSeconds"`
	Metric      string            `json:"metric"`
	Owners      []usageAtOwnerDTO `json:"owners"`
}

type usagePointDTO struct {
	T          int64   `json:"t"`
	CPUPercent float64 `json:"cpuPercent"`
	MemBytes   uint64  `json:"memBytes"`
}

type usageHistoryResponse struct {
	StepSeconds int             `json:"stepSeconds"`
	Points      []usagePointDTO `json:"points"`
}

func (h usageHandlers) clock() time.Time {
	if h.now != nil {
		return h.now()
	}
	return time.Now()
}

func (h usageHandlers) current(w http.ResponseWriter, _ *http.Request) {
	if h.latest == nil {
		usageNotReady(w)
		return
	}
	snap, ok := h.latest.Latest()
	if !ok {
		usageNotReady(w)
		return
	}
	out := usageResponse{Time: snap.Time, DockerOK: snap.DockerOK, Owners: make([]usageOwnerDTO, 0, len(snap.Owners))}
	for _, o := range snap.Owners {
		d := usageOwnerDTO{
			Key: o.Key, Kind: string(o.Kind), Label: o.Label, Managed: o.Managed,
			CPUPercent: o.CPUPercent, MemBytes: o.MemBytes, ProcCount: o.ProcCount, Truncated: o.Truncated,
			Processes: make([]usageProcDTO, 0, len(o.Processes)),
		}
		for _, p := range o.Processes {
			d.Processes = append(d.Processes, usageProcDTO{PID: p.PID, Name: p.Name, Cmd: p.Cmd, User: p.User,
				CPUPercent: p.CPUPercent, MemBytes: p.MemBytes})
		}
		out.Owners = append(out.Owners, d)
	}
	WriteJSON(w, http.StatusOK, out)
}

func usageNotReady(w http.ResponseWriter) {
	WriteError(w, http.StatusServiceUnavailable, "USAGE_NOT_READY", "O consumo por dono ainda não está disponível.")
}

func invalidRange(w http.ResponseWriter) {
	WriteError(w, http.StatusBadRequest, "INVALID_RANGE", "Intervalo inválido; use 1h, 24h ou 7d.")
}

func (h usageHandlers) at(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	t, err := strconv.ParseInt(q.Get("t"), 10, 64)
	if err != nil || t < 0 || t > h.clock().Add(usageFutureSlack).Unix() {
		WriteError(w, http.StatusBadRequest, "INVALID_TIME", "Instante inválido; use segundos unix, sem datas futuras.")
		return
	}
	spec, ok := metricRanges[q.Get("range")]
	if !ok {
		invalidRange(w)
		return
	}
	metric := q.Get("metric")
	if metric == "" {
		metric = "cpu"
	}
	if metric != "cpu" && metric != "mem" {
		WriteError(w, http.StatusBadRequest, "INVALID_METRIC", "Métrica inválida; use cpu ou mem.")
		return
	}
	step := int64(spec.step / time.Second)
	from := t - t%step
	rows, err := h.db.UsageAt(r.Context(), time.Unix(from, 0), time.Unix(from+step, 0), spec.step, metric, usageAtLimit)
	if errors.Is(err, store.ErrInvalidMetric) {
		WriteError(w, http.StatusBadRequest, "INVALID_METRIC", "Métrica inválida; use cpu ou mem.")
		return
	}
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "Não foi possível ler o consumo.")
		return
	}
	owners := make([]usageAtOwnerDTO, 0, len(rows))
	for _, u := range rows {
		owners = append(owners, usageAtOwnerDTO{Key: u.Key, Kind: u.Kind, Label: u.Label, CPUPercent: u.CPUPercent, MemBytes: u.MemBytes})
	}
	WriteJSON(w, http.StatusOK, usageAtResponse{T: from, StepSeconds: int(step), Metric: metric, Owners: owners})
}

func (h usageHandlers) history(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	owner := q.Get("owner")
	if owner == "" || len(owner) > maxOwnerKeyLen || !ownerKeyRe.MatchString(owner) {
		WriteError(w, http.StatusBadRequest, "INVALID_OWNER", "Dono inválido.")
		return
	}
	spec, ok := metricRanges[q.Get("range")]
	if !ok {
		invalidRange(w)
		return
	}
	to := h.clock()
	rows, err := h.db.UsageHistory(r.Context(), owner, to.Add(-spec.window), to, spec.step)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "Não foi possível ler o histórico de consumo.")
		return
	}
	pts := make([]usagePointDTO, 0, len(rows))
	for _, u := range rows {
		pts = append(pts, usagePointDTO{T: u.TS, CPUPercent: u.CPUPercent, MemBytes: u.MemBytes})
	}
	WriteJSON(w, http.StatusOK, usageHistoryResponse{StepSeconds: int(spec.step / time.Second), Points: pts})
}
