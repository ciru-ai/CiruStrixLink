package ui

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

type modelRate struct {
	TokensPerSecond float64   `json:"tokens_per_second"`
	Basis           string    `json:"basis"`
	MeasuredAt      time.Time `json:"measured_at"`
}

type draftAcceptance struct {
	Percent    float64   `json:"percent"`
	Accepted   float64   `json:"accepted"`
	Proposed   float64   `json:"proposed"`
	Basis      string    `json:"basis"`
	MeasuredAt time.Time `json:"measured_at"`
}

type modelHistoryPoint struct {
	At time.Time `json:"at"`
	TG *float64  `json:"tg"`
	PP *float64  `json:"pp"`
}

type modelStatus struct {
	State       string              `json:"state"`
	Name        string              `json:"name,omitempty"`
	APIHost     string              `json:"api_host,omitempty"`
	Endpoint    string              `json:"endpoint,omitempty"`
	Context     int                 `json:"context_window,omitempty"`
	CollectedAt time.Time           `json:"collected_at"`
	Metrics     map[string]float64  `json:"metrics,omitempty"`
	TG          *modelRate          `json:"tg,omitempty"`
	PP          *modelRate          `json:"pp,omitempty"`
	LiveTG      *modelRate          `json:"live_tg,omitempty"`
	Speculation string              `json:"speculation,omitempty"`
	Acceptance  *draftAcceptance    `json:"acceptance,omitempty"`
	History     []modelHistoryPoint `json:"history,omitempty"`
	Note        string              `json:"note,omitempty"`
}

type modelMonitor struct {
	mu            sync.Mutex
	base, apiHost string
	token         string
	client        *http.Client
	last          modelStatus
	history       []modelHistoryPoint
	historyRun    string
}

func newModelMonitor(endpoint, token, localHost string) (*modelMonitor, error) {
	if endpoint == "" {
		return nil, nil
	}
	u, err := url.Parse(endpoint)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("model URL must be an http(s) frontend URL without credentials or query parameters")
	}
	u.Path = strings.TrimSuffix(strings.TrimRight(u.Path, "/"), "/v1")
	host := u.Hostname()
	if ip := net.ParseIP(host); host == "localhost" || (ip != nil && ip.IsLoopback()) {
		host = localHost
	}
	if u.Port() != "" {
		host += ":" + u.Port()
	}
	return &modelMonitor{base: u.String(), apiHost: host, token: token, client: &http.Client{Timeout: 3 * time.Second}}, nil
}

func (c *Console) handleModel(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if c.model == nil || c.filesMode() {
		writeJSON(w, http.StatusOK, modelStatus{State: "unconfigured", CollectedAt: time.Now().UTC()})
		return
	}
	s := c.model.collect(r.Context())
	if s.Speculation == "" && c.launch != nil {
		var runtime launchNodeStatus
		c.launch.readRuntimeSettings(&runtime)
		if runtime.DFlashKnown {
			s.Speculation = dflashLabel(runtime.DFlashTokens)
		}
	}
	writeJSON(w, http.StatusOK, s)
}

// Only the operator-configured frontend is read. No generation, discovery,
// per-rank summation, or browser-supplied upstream URLs are involved.
func (m *modelMonitor) get(ctx context.Context, path string) ([]byte, error) {
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, m.base+path, nil)
	if err != nil {
		return nil, err
	}
	if m.token != "" {
		r.Header.Set("Authorization", "Bearer "+m.token)
	}
	resp, err := m.client.Do(r)
	if err != nil {
		return nil, fmt.Errorf("model frontend is unreachable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s returned HTTP %d", path, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 4<<20))
}

func (m *modelMonitor) collect(ctx context.Context) modelStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	if time.Since(m.last.CollectedAt) < 3*time.Second {
		return m.last
	}
	s := modelStatus{State: "unreachable", APIHost: m.apiHost, Endpoint: m.base + "/v1", CollectedAt: time.Now().UTC(), Speculation: os.Getenv("CIRU_STRIXLINK_MODEL_SPECULATION")}
	var modelBody, metricsBody []byte
	var modelErr, metricsErr error
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); modelBody, modelErr = m.get(ctx, "/v1/models") }()
	go func() { defer wg.Done(); metricsBody, metricsErr = m.get(ctx, "/metrics") }()
	wg.Wait()
	var models struct {
		Data []struct {
			ID      string `json:"id"`
			Context int    `json:"max_model_len"`
		} `json:"data"`
	}
	if modelErr != nil {
		s.Note = modelErr.Error()
	} else if json.Unmarshal(modelBody, &models) != nil || len(models.Data) == 0 {
		s.Note = "The frontend has not reported a loaded model."
	} else {
		s.State, s.Name, s.Context = "connected", models.Data[0].ID, models.Data[0].Context
		if metricsErr != nil {
			s.Note = "Model connected; performance metrics are unavailable."
		} else {
			s.Metrics = parseModelMetrics(string(metricsBody), s.Name)
			applyModelRates(&s, m.last)
			applyDraftAcceptance(&s, m.last)
			if len(s.Metrics) == 0 {
				s.Note = "Model connected; supported performance metrics are unavailable."
			}
		}
	}
	m.recordHistory(&s)
	m.last = s
	return s
}

var modelMetricLine = regexp.MustCompile(`^([a-zA-Z_:][a-zA-Z0-9_:]*)(?:\{(.*)\})?\s+([^\s]+)`)
var modelMetricLabel = regexp.MustCompile(`(?:^|,)\s*(model_name|engine)="((?:\\.|[^"\\])*)"`)

var modelMetricNames = map[string]string{
	"process_start_time_seconds":                  "started_at",
	"vllm:num_requests_running":                   "running",
	"vllm:num_requests_waiting":                   "waiting",
	"vllm:generation_tokens_total":                "generated",
	"vllm:request_generation_tokens_count":        "completed",
	"vllm:request_generation_tokens_sum":          "completed_tokens",
	"vllm:request_decode_time_seconds_sum":        "decode_seconds",
	"vllm:request_prefill_kv_computed_tokens_sum": "prefill_tokens",
	"vllm:request_prefill_time_seconds_sum":       "prefill_seconds",
	"vllm:spec_decode_num_draft_tokens_total":     "draft_proposed",
	"vllm:spec_decode_num_accepted_tokens_total":  "draft_accepted",
}

func parseModelMetrics(body, model string) map[string]float64 {
	out := map[string]float64{}
	scan := bufio.NewScanner(strings.NewReader(body))
	for scan.Scan() {
		match := modelMetricLine.FindStringSubmatch(scan.Text())
		if len(match) == 0 {
			continue
		}
		key, wanted := modelMetricNames[match[1]]
		if !wanted {
			continue
		}
		selected := true
		for _, label := range modelMetricLabel.FindAllStringSubmatch(match[2], -1) {
			value, err := strconv.Unquote(`"` + label[2] + `"`)
			if err != nil || (label[1] == "model_name" && value != model) || (label[1] == "engine" && value != "0") {
				selected = false
			}
		}
		value, err := strconv.ParseFloat(match[3], 64)
		if selected && err == nil && !math.IsNaN(value) && !math.IsInf(value, 0) {
			out[key] = value
		}
	}
	return out
}

func modelRatio(tokens, seconds float64, basis string, at time.Time) *modelRate {
	if tokens < 0 || seconds <= 0 {
		return nil
	}
	return &modelRate{TokensPerSecond: tokens / seconds, Basis: basis, MeasuredAt: at}
}

func applyModelRates(s *modelStatus, previous modelStatus) {
	v, old := s.Metrics, previous.Metrics
	continuous := s.Name == previous.Name && previous.State == "connected" && v["started_at"] == old["started_at"] && v["completed"] >= old["completed"] && v["generated"] >= old["generated"]
	if continuous {
		s.TG, s.PP = previous.TG, previous.PP
		elapsed := s.CollectedAt.Sub(previous.CollectedAt).Seconds()
		_, generatedKnown := v["generated"]
		if elapsed > 0 && elapsed < 30 && generatedKnown {
			s.LiveTG = modelRatio(v["generated"]-old["generated"], elapsed, fmt.Sprintf("Live output · %.0fs", elapsed), s.CollectedAt)
		}
		if n := v["completed"] - old["completed"]; n > 0 {
			basis := "Last request"
			if n > 1 {
				basis = fmt.Sprintf("Last %.0f requests", n)
			}
			// Remove one first token per completed request: that token belongs
			// to prefill, not the measured first-to-last decode interval.
			s.TG = modelRatio(v["completed_tokens"]-old["completed_tokens"]-n, v["decode_seconds"]-old["decode_seconds"], basis, s.CollectedAt)
			if _, ok := v["prefill_tokens"]; ok {
				s.PP = modelRatio(v["prefill_tokens"]-old["prefill_tokens"], v["prefill_seconds"]-old["prefill_seconds"], basis, s.CollectedAt)
			}
		}
	} else if v["completed"] > 0 {
		s.TG = modelRatio(v["completed_tokens"]-v["completed"], v["decode_seconds"], "Since engine start", s.CollectedAt)
		if _, ok := v["prefill_tokens"]; ok {
			s.PP = modelRatio(v["prefill_tokens"], v["prefill_seconds"], "Since engine start", s.CollectedAt)
		}
	}
}

func acceptanceRatio(accepted, proposed float64, basis string, at time.Time) *draftAcceptance {
	if proposed <= 0 || accepted < 0 || accepted > proposed {
		return nil
	}
	return &draftAcceptance{Percent: 100 * accepted / proposed, Accepted: accepted, Proposed: proposed, Basis: basis, MeasuredAt: at}
}

func applyDraftAcceptance(s *modelStatus, previous modelStatus) {
	v, old := s.Metrics, previous.Metrics
	accepted, known := v["draft_accepted"]
	proposed, proposedKnown := v["draft_proposed"]
	if !known || !proposedKnown {
		return
	}
	_, oldKnown := old["draft_proposed"]
	continuous := oldKnown && s.Name == previous.Name && previous.State == "connected" && v["started_at"] == old["started_at"] && proposed >= old["draft_proposed"] && accepted >= old["draft_accepted"]
	if continuous {
		s.Acceptance = previous.Acceptance
		if proposed > old["draft_proposed"] {
			s.Acceptance = acceptanceRatio(accepted-old["draft_accepted"], proposed-old["draft_proposed"], "Latest reported drafts", s.CollectedAt)
		}
	} else {
		s.Acceptance = acceptanceRatio(accepted, proposed, "Since engine start", s.CollectedAt)
	}
}

// History lives only in the console's memory. Page reloads retain it; a new
// model or engine starts a fresh series. No prompt content is stored.
func (m *modelMonitor) recordHistory(s *modelStatus) {
	if s.State == "connected" && len(s.Metrics) > 1 {
		run := fmt.Sprintf("%s/%g", s.Name, s.Metrics["started_at"])
		if (m.historyRun != "" && run != m.historyRun) || (m.last.State == "connected" && s.Metrics["generated"] < m.last.Metrics["generated"]) {
			m.history = nil
		}
		m.historyRun = run
	}
	point := modelHistoryPoint{At: s.CollectedAt}
	if s.LiveTG != nil {
		point.TG = &s.LiveTG.TokensPerSecond
	}
	if s.PP != nil && s.PP.MeasuredAt.Equal(s.CollectedAt) && strings.HasPrefix(s.PP.Basis, "Last ") {
		point.PP = &s.PP.TokensPerSecond
	}
	m.history = append(m.history, point)
	cutoff := s.CollectedAt.Add(-10 * time.Minute)
	for len(m.history) > 0 && (m.history[0].At.Before(cutoff) || len(m.history) > 201) {
		m.history = m.history[1:]
	}
	s.History = append([]modelHistoryPoint(nil), m.history...)
}
