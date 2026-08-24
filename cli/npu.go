package cli

// NPU telemetry integration.
//
// The standalone hailo-monitor tool streams one JSON snapshot per line
// (--json). This file tails that JSONL file and exposes it to the WebUI:
//
// - GET /api/npu/metrics : latest snapshot + data age + running chat models
// - GET /api/npu/history : ring buffer of recent snapshots (?limit=N)
// - GET /api/npu/stream  : Server-Sent Events feed of live snapshots
//
// The metrics file is produced by the standalone service
// (deploy/hailo-npu-metrics.service → /var/lib/hailo-npu/metrics.jsonl).
// Resolution order:
//
//  1. explicit path passed to RunWebUI (webui <addr> <metrics.jsonl>)
//  2. HAILO_NPU_METRICS environment variable
//  3. /var/lib/hailo-npu/metrics.jsonl (default)
//
// Additionally the inference server's GET /api/ps is polled so the UI can
// show WHICH chat model is currently loaded alongside the chip-wide
// utilization counters.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"hailo-ollama-cli/client"
)

// ThrottlingLevel mirrors one entry of the firmware thermal-protection table.
type ThrottlingLevel struct {
	ThresholdC  float64 `json:"threshold_c"`
	HysteresisC float64 `json:"hysteresis_c"`
	NNClockKHz  uint32  `json:"nn_clock_khz"`
}

// NpuEvent is a fault/health notification captured by hailo-monitor.
type NpuEvent struct {
	Time   string `json:"time"`
	Name   string `json:"name"`
	Detail string `json:"detail,omitempty"`
}

// NpuFeatures lists hardware capabilities reported by the firmware.
type NpuFeatures struct {
	Ethernet          bool `json:"ethernet"`
	Mipi              bool `json:"mipi"`
	Pcie              bool `json:"pcie"`
	CurrentMonitoring bool `json:"current_monitoring"`
	Mdio              bool `json:"mdio"`
	PowerMeasurement  bool `json:"power_measurement"`
}

// NpuPCIe describes the PCI express link training state.
type NpuPCIe struct {
	LinkCur  string `json:"link_cur"`
	LinkMax  string `json:"link_max"`
	WidthCur uint32 `json:"width_cur"`
	WidthMax uint32 `json:"width_max"`
}

// NpuSnapshot mirrors hailo-monitor --json output. Nullable readings come
// through as JSON null and map to pointer fields here.
type NpuSnapshot struct {
	DeviceID     string `json:"device_id"`
	Architecture string `json:"architecture"`
	FwVersion    string `json:"fw_version"`
	Serial       string `json:"serial"`
	PartNumber   string `json:"part_number"`
	Product      string `json:"product"`
	Connected    bool   `json:"connected"`

	PowerSensor   bool `json:"power_sensor"`
	CurrentSensor bool `json:"current_sensor"`
	TempSensor    bool `json:"temp_sensor"`

	Ts0C             *float64 `json:"ts0_c"`
	Ts1C             *float64 `json:"ts1_c"`
	OnDieC           *float64 `json:"on_die_c"`
	PowerMeasurement *float64 `json:"power_measurement"`
	CpuUtilization   *float64 `json:"cpu_utilization"`
	NNCUtilization   *float64 `json:"nnc_utilization"`
	RamTotalKiB      int64    `json:"ram_total_kib"`
	RamUsedKiB       int64    `json:"ram_used_kib"`

	TempThrottlingActive     bool `json:"temp_throttling"`
	OvercurrentThrottlingAct bool `json:"overcurrent_throttling"`
	OvercurrentProtectActive bool `json:"overcurrent_protect"`

	WorkloadActive bool    `json:"workload_active"`
	Model          string  `json:"model"`
	FPS            float64 `json:"fps"`
	LatencyMS      float64 `json:"latency_ms"`
	CapacityFPS    float64 `json:"capacity_fps"`
	LoadPercent    float64 `json:"load_percent"`

	FwIsRelease         bool              `json:"fw_is_release"`
	ProtocolVersion     uint32            `json:"protocol_version"`
	LoggerVersion       uint32            `json:"logger_version"`
	BoardName           string            `json:"board_name"`
	BootSource          string            `json:"boot_source"`
	Lcs                 uint32            `json:"lcs"`
	NNCoreClockHz       uint32            `json:"nn_core_clock_hz"`
	DriverVersion       string            `json:"driver_version"`
	KernelRelease       string            `json:"kernel_release"`
	SocID               string            `json:"soc_id"`
	EthMAC              string            `json:"eth_mac"`
	UnitTrackingID      string            `json:"unit_tracking_id"`
	GPIOMask            uint16            `json:"gpio_mask"`
	Features            NpuFeatures       `json:"features"`
	PCIe                NpuPCIe           `json:"pcie"`
	OnDieVoltageMV      *float64          `json:"on_die_voltage_mv"`
	BistFailureMask     int32             `json:"bist_failure_mask"`
	OCZone              int32             `json:"oc_zone"`
	TempZone            int32             `json:"temp_zone"`
	OrangeTempC         *int32            `json:"orange_temp_c"`
	OrangeHystC         *int32            `json:"orange_hyst_c"`
	RedTempC            *int32            `json:"red_temp_c"`
	RedHystC            *int32            `json:"red_hyst_c"`
	RedOCThreshold      *float64          `json:"red_oc_threshold"`
	RequestedOCClkKHz   uint32            `json:"requested_oc_clk_khz"`
	RequestedTempClkKHz uint32            `json:"requested_temp_clk_khz"`
	ThrottlingLevels    []ThrottlingLevel `json:"throttling_levels"`

	InvalidFrames uint64     `json:"invalid_frames"`
	HwLatencyMS   *float64   `json:"hw_latency_ms"`
	EventsTotal   uint64     `json:"events_total"`
	Events        []NpuEvent `json:"events"`
}

// NpuMetricsEnvelope wraps a snapshot with freshness information so the UI
// can detect a stalled producer.
type NpuMetricsEnvelope struct {
	ReceivedAt time.Time    `json:"received_at"`
	AgeMS      int64        `json:"age_ms"`
	Producer   string       `json:"producer"`
	Snapshot   *NpuSnapshot `json:"snapshot"`
	// RunningModels lists the chat models the inference server currently has
	// loaded (GET /api/ps). Empty when none are resident or the server is down.
	RunningModels []string `json:"running_models,omitempty"`
}

// npuStore holds the latest snapshot, a bounded history ring and SSE
// subscribers. All methods are safe for concurrent use.
type npuStore struct {
	mu       sync.RWMutex
	latest   *NpuSnapshot
	gotAt    time.Time
	history  []NpuSnapshot
	maxHist  int
	seq      uint64
	producer string
	running  []string // chat models currently loaded on the server (from /api/ps)
	subs     map[chan []byte]struct{}
}

func newNpuStore(maxHist int) *npuStore {
	return &npuStore{maxHist: maxHist, subs: make(map[chan []byte]struct{})}
}

func (s *npuStore) setProducer(p string) {
	s.mu.Lock()
	s.producer = p
	s.mu.Unlock()
}

func (s *npuStore) setRunning(names []string) {
	s.mu.Lock()
	s.running = names
	s.mu.Unlock()
}

// publish ingests one raw JSONL line. Undecodable lines (e.g. a partially
// flushed write caught mid-append) are skipped; the next tick sees them whole.
func (s *npuStore) publish(raw []byte) {
	snap := &NpuSnapshot{}
	if err := json.Unmarshal(bytes.TrimSpace(raw), snap); err != nil {
		return
	}
	s.mu.Lock()
	s.latest = snap
	s.gotAt = time.Now()
	s.seq++
	s.history = append(s.history, *snap)
	if len(s.history) > s.maxHist {
		s.history = s.history[len(s.history)-s.maxHist:]
	}
	s.mu.Unlock()
	s.broadcast(raw)
}

func (s *npuStore) broadcast(raw []byte) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for ch := range s.subs {
		select {
		case ch <- raw:
		default: // slow subscriber: drop this frame, it polls anyway
		}
	}
}

func (s *npuStore) subscribe() (<-chan []byte, func()) {
	ch := make(chan []byte, 8)
	s.mu.Lock()
	s.subs[ch] = struct{}{}
	s.mu.Unlock()
	return ch, func() {
		s.mu.Lock()
		delete(s.subs, ch)
		s.mu.Unlock()
	}
}

func (s *npuStore) envelope() NpuMetricsEnvelope {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e := NpuMetricsEnvelope{
		ReceivedAt:    s.gotAt,
		Producer:      s.producer,
		Snapshot:      s.latest,
		RunningModels: append([]string(nil), s.running...),
	}
	if !s.gotAt.IsZero() {
		e.AgeMS = time.Since(s.gotAt).Milliseconds()
	} else if e.Snapshot == nil {
		e.AgeMS = -1
	}
	return e
}

// followNpuFile tails path like `tail -F`: reopens on rotation/truncation,
// jumps near the end on first open so a day-old file does not replay.
func followNpuFile(ctx context.Context, path string, st *npuStore) {
	var offset int64 = -1
	tick := time.NewTicker(300 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		f, err := os.Open(path)
		if err != nil {
			continue // producer not started yet
		}
		fi, err := f.Stat()
		if err != nil {
			f.Close()
			continue
		}
		size := fi.Size()
		switch {
		case offset < 0:
			start := size - 65536
			if start < 0 {
				start = 0
			}
			buf := make([]byte, size-start)
			if _, err := f.ReadAt(buf, start); err == nil || err == io.EOF {
				for _, ln := range bytes.Split(bytes.TrimRight(buf, "\n"), []byte("\n")) {
					if len(ln) > 0 {
						st.publish(ln)
					}
				}
			}
			offset = size
		case size > offset:
			buf := make([]byte, size-offset)
			if _, err := f.ReadAt(buf, offset); err == nil || err == io.EOF {
				for _, ln := range bytes.Split(bytes.TrimRight(buf, "\n"), []byte("\n")) {
					if len(ln) > 0 {
						st.publish(ln)
					}
				}
			}
			offset = size
		case size < offset: // truncated/rotated
			offset = 0
		}
		f.Close()
	}
}

// resolveMetricsPath applies the documented precedence for the JSONL file.
func resolveMetricsPath(explicit string) string {
	if explicit != "" {
		return explicit
	}
	if p := os.Getenv("HAILO_NPU_METRICS"); p != "" {
		return p
	}
	return "/var/lib/hailo-npu/metrics.jsonl"
}

// pollServerModels periodically asks the inference server which chat models
// are currently loaded (GET /api/ps) so the UI can attribute NPU activity to
// a concrete model. Errors are swallowed: the last known list is kept and an
// unreachable server simply yields an empty list.
func pollServerModels(ctx context.Context, apiCli *client.Client, st *npuStore, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()

	poll := func() {
		pctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		models, err := apiCli.ListRunningModels(pctx)
		if err != nil {
			st.setRunning(nil) // server unreachable/down
			return
		}
		names := make([]string, 0, len(models))
		for _, m := range models {
			names = append(names, m.Name)
		}
		st.setRunning(names)
	}

	poll()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			poll()
		}
	}
}

// startNPUSource resolves the metrics file, starts the tailer and the
// server-model poller. Callers attach the returned source's handlers to
// their mux.
func startNPUSource(ctx context.Context, apiCli *client.Client, explicitPath string) *npuStore {
	st := newNpuStore(3600) // ~1h of 1s samples
	st.setProducer("file-tail")

	go followNpuFile(ctx, resolveMetricsPath(explicitPath), st)
	if apiCli != nil {
		go pollServerModels(ctx, apiCli, st, 5*time.Second)
	}
	return st
}

// ---- HTTP handlers ----

func (s *npuStore) handleMetrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s.envelope())
}

func (s *npuStore) handleHistory(w http.ResponseWriter, r *http.Request) {
	limit := 300
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 3600 {
			limit = n
		}
	}
	s.mu.RLock()
	hist := s.history
	if len(hist) > limit {
		hist = hist[len(hist)-limit:]
	}
	out := make([]NpuSnapshot, len(hist))
	copy(out, hist)
	s.mu.RUnlock()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

func (s *npuStore) handleStream(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	fmt.Fprint(w, ": connected\n\n")
	fl.Flush()

	ch, unsub := s.subscribe()
	defer unsub()
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			fmt.Fprint(w, ": keepalive\n\n")
			fl.Flush()
		case raw := <-ch:
			fmt.Fprintf(w, "event: snapshot\ndata: %s\n\n", raw)
			fl.Flush()
		}
	}
}
