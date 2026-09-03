package ui

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestModelMonitorReadsFrontendMetricsOnce(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("monitor used %s", r.Method)
		}
		switch r.URL.Path {
		case "/v1/models":
			fmt.Fprint(w, `{"data":[{"id":"glm","max_model_len":65536}]}`)
		case "/metrics":
			fmt.Fprint(w, `process_start_time_seconds 10
vllm:num_requests_running{engine="0",model_name="glm"} 0
vllm:generation_tokens_total{model_name="glm",engine="0"} 101
vllm:request_generation_tokens_count{model_name="glm",engine="0"} 1
vllm:request_generation_tokens_sum{model_name="glm",engine="0"} 101
vllm:request_decode_time_seconds_sum{model_name="glm",engine="0"} 5
vllm:request_prefill_kv_computed_tokens_sum{model_name="glm",engine="0"} 600
vllm:request_prefill_time_seconds_sum{model_name="glm",engine="0"} 2
vllm:spec_decode_num_draft_tokens_total{model_name="glm",engine="0"} 100
vllm:spec_decode_num_accepted_tokens_total{model_name="glm",engine="0"} 40
vllm:request_generation_tokens_sum{model_name="other",engine="0"} 999
vllm:request_generation_tokens_sum{model_name="glm",engine="1"} 999
`)
		default:
			t.Errorf("unexpected request to %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	m, err := newModelMonitor(upstream.URL+"/v1", "", "sozo")
	if err != nil {
		t.Fatal(err)
	}
	s := m.collect(context.Background())
	if s.State != "connected" || s.Name != "glm" || s.TG == nil || s.PP == nil || s.TG.TokensPerSecond != 20 || s.PP.TokensPerSecond != 300 {
		t.Fatalf("unexpected model status: %#v, TG=%#v, PP=%#v", s, s.TG, s.PP)
	}
	if s.Acceptance == nil || s.Acceptance.Percent != 40 || len(s.History) != 1 || s.History[0].TG != nil {
		t.Fatalf("acceptance=%#v history=%#v", s.Acceptance, s.History)
	}
}

func TestModelAcceptanceAndSpeedHistory(t *testing.T) {
	m := &modelMonitor{}
	before := modelStatus{State: "connected", Name: "glm", CollectedAt: time.Unix(100, 0), Metrics: map[string]float64{
		"started_at": 1, "generated": 100, "draft_proposed": 70, "draft_accepted": 35,
	}}
	applyDraftAcceptance(&before, modelStatus{})
	m.recordHistory(&before)
	m.last = before
	after := modelStatus{State: "connected", Name: "glm", CollectedAt: time.Unix(105, 0), Metrics: map[string]float64{
		"started_at": 1, "generated": 230, "draft_proposed": 140, "draft_accepted": 77,
	}}
	applyModelRates(&after, before)
	applyDraftAcceptance(&after, before)
	m.recordHistory(&after)
	if after.Acceptance == nil || after.Acceptance.Percent != 60 || after.Acceptance.Proposed != 70 || after.History[1].TG == nil || *after.History[1].TG != 26 {
		t.Fatalf("acceptance=%#v history=%#v", after.Acceptance, after.History)
	}
	quiet := modelStatus{State: "connected", Name: "glm", CollectedAt: time.Unix(110, 0), Metrics: after.Metrics}
	applyDraftAcceptance(&quiet, after)
	if quiet.Acceptance != after.Acceptance {
		t.Fatal("no new drafts must retain the labeled previous sample, not invent zero acceptance")
	}
	m.last = after
	restarted := modelStatus{State: "connected", Name: "glm", CollectedAt: time.Unix(115, 0), Metrics: map[string]float64{"started_at": 2, "generated": 0}}
	m.recordHistory(&restarted)
	if len(restarted.History) != 1 || restarted.History[0].TG != nil {
		t.Fatalf("new engine retained old history: %#v", restarted.History)
	}
}

func TestModelRatesSeparateLiveOutputFromDecodeAndPrefill(t *testing.T) {
	before := modelStatus{State: "connected", Name: "glm", CollectedAt: time.Unix(100, 0), Metrics: map[string]float64{
		"started_at": 1, "running": 1, "generated": 101, "completed": 1,
		"completed_tokens": 101, "decode_seconds": 5, "prefill_tokens": 100, "prefill_seconds": 1,
	}}
	after := modelStatus{State: "connected", Name: "glm", CollectedAt: time.Unix(105, 0), Metrics: map[string]float64{
		"started_at": 1, "running": 1, "generated": 262, "completed": 2,
		"completed_tokens": 262, "decode_seconds": 10, "prefill_tokens": 420, "prefill_seconds": 2,
	}}
	applyModelRates(&after, before)
	if after.TG == nil || after.TG.TokensPerSecond != 32 || after.TG.Basis != "Last request" || after.PP == nil || after.PP.TokensPerSecond != 320 || after.LiveTG == nil || after.LiveTG.TokensPerSecond != 32.2 {
		t.Fatalf("TG=%#v PP=%#v live=%#v", after.TG, after.PP, after.LiveTG)
	}
	after.Metrics["started_at"] = 2
	applyModelRates(&after, before)
	if after.TG.Basis != "Since engine start" {
		t.Fatalf("restart reused a rate from another engine: %#v", after.TG)
	}
}
