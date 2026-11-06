package api

import (
	"net/http"
	"time"

	"github.com/engviniciusbs/korego/internal/node/metrics"
	"github.com/engviniciusbs/korego/internal/store"
)

// LatestMetrics fornece a última amostra de métricas (implementada por monitor.Collector).
type LatestMetrics interface {
	Latest() (metrics.Sample, bool)
}

type metricsHandlers struct {
	latest LatestMetrics
	db     *store.DB
	now    func() time.Time
}

type rangeSpec struct {
	window time.Duration
	step   time.Duration
}

var metricRanges = map[string]rangeSpec{
	"1h":  {time.Hour, time.Minute},
	"24h": {24 * time.Hour, 5 * time.Minute},
	"7d":  {7 * 24 * time.Hour, 30 * time.Minute},
}

type metricPoint struct {
	T                int64   `json:"t"`
	CPUPercent       float64 `json:"cpuPercent"`
	MemUsedBytes     uint64  `json:"memUsedBytes"`
	MemTotalBytes    uint64  `json:"memTotalBytes"`
	Load1            float64 `json:"load1"`
	NetRxBytesPerSec float64 `json:"netRxBytesPerSec"`
	NetTxBytesPerSec float64 `json:"netTxBytesPerSec"`
	DiskUsedBytes    uint64  `json:"diskUsedBytes"`
	DiskTotalBytes   uint64  `json:"diskTotalBytes"`
}

type metricHistoryResponse struct {
	Range       string        `json:"range"`
	StepSeconds int           `json:"stepSeconds"`
	Points      []metricPoint `json:"points"`
}

func (h metricsHandlers) current(w http.ResponseWriter, _ *http.Request) {
	if h.latest != nil {
		if s, ok := h.latest.Latest(); ok {
			WriteJSON(w, http.StatusOK, s)
			return
		}
	}
	WriteError(w, http.StatusServiceUnavailable, "METRICS_NOT_READY", "As métricas ainda não estão disponíveis.")
}

func (h metricsHandlers) history(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("range")
	spec, ok := metricRanges[name]
	if !ok {
		WriteError(w, http.StatusBadRequest, "INVALID_RANGE", "Intervalo inválido; use 1h, 24h ou 7d.")
		return
	}
	now := time.Now
	if h.now != nil {
		now = h.now
	}
	to := now()
	rows, err := h.db.MetricHistory(r.Context(), to.Add(-spec.window), to, spec.step)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "Não foi possível ler o histórico de métricas.")
		return
	}
	pts := make([]metricPoint, 0, len(rows))
	for _, m := range rows {
		pts = append(pts, metricPoint{
			T: m.TS, CPUPercent: m.CPUPercent, MemUsedBytes: m.MemUsed, MemTotalBytes: m.MemTotal,
			Load1: m.Load1, NetRxBytesPerSec: m.NetRx, NetTxBytesPerSec: m.NetTx,
			DiskUsedBytes: m.DiskUsed, DiskTotalBytes: m.DiskTotal,
		})
	}
	WriteJSON(w, http.StatusOK, metricHistoryResponse{Range: name, StepSeconds: int(spec.step / time.Second), Points: pts})
}
