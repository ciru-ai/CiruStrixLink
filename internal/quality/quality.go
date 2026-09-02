// Package quality turns measured link behavior into a conservative runtime policy.
package quality

import "math"

// Metrics are the application-visible measurements used for classification.
type Metrics struct {
	UploadGbps      float64 `json:"upload_gbps"`
	DownloadGbps    float64 `json:"download_gbps"`
	RTTP99Ms        float64 `json:"rtt_p99_ms"`
	ReconnectPassed int     `json:"reconnect_passed"`
	ReconnectTotal  int     `json:"reconnect_total"`
	IntegrityUpload bool    `json:"integrity_upload"`
	IntegrityDown   bool    `json:"integrity_download"`
}

// Policy is deliberately transport-neutral. Model launchers can consume it
// without depending on the benchmark implementation.
type Policy struct {
	Class               string `json:"class"`
	Ready               bool   `json:"ready"`
	HeartbeatIntervalMs int    `json:"heartbeat_interval_ms"`
	PeerTimeoutMs       int    `json:"peer_timeout_ms"`
	ReconnectAttempts   int    `json:"reconnect_attempts"`
	MaxInFlight         int    `json:"max_in_flight"`
	SuggestedChunkBytes int    `json:"suggested_chunk_bytes"`
	Reason              string `json:"reason"`
}

// Classify uses the weaker direction. A USB4 link can be highly asymmetric,
// and its nominal rate is not an application throughput guarantee.
func Classify(m Metrics) Policy {
	minGbps := math.Min(m.UploadGbps, m.DownloadGbps)
	passed := m.ReconnectTotal > 0 && m.ReconnectPassed == m.ReconnectTotal && m.IntegrityUpload && m.IntegrityDown

	if passed && minGbps >= 12 && m.RTTP99Ms <= 2 {
		return Policy{Class: "excellent", Ready: true, HeartbeatIntervalMs: 2000, PeerTimeoutMs: 10000, ReconnectAttempts: 5, MaxInFlight: 2, SuggestedChunkBytes: 8 << 20, Reason: "both directions are fast, low-latency, reconnectable, and data-clean"}
	}
	if passed && minGbps >= 5 && m.RTTP99Ms <= 5 {
		return Policy{Class: "good", Ready: true, HeartbeatIntervalMs: 1500, PeerTimeoutMs: 15000, ReconnectAttempts: 8, MaxInFlight: 2, SuggestedChunkBytes: 4 << 20, Reason: "the weaker direction is suitable for a bounded two-slot pipeline"}
	}
	if passed && minGbps >= 1 && m.RTTP99Ms <= 15 {
		return Policy{Class: "constrained", Ready: true, HeartbeatIntervalMs: 1000, PeerTimeoutMs: 30000, ReconnectAttempts: 12, MaxInFlight: 2, SuggestedChunkBytes: 1 << 20, Reason: "the link is usable with smaller chunks and longer deadlines"}
	}
	reason := "link failed the public readiness gate"
	if !passed {
		reason = "integrity or reconnect checks failed"
	}
	return Policy{Class: "degraded", Ready: false, HeartbeatIntervalMs: 500, PeerTimeoutMs: 45000, ReconnectAttempts: 20, MaxInFlight: 1, SuggestedChunkBytes: 256 << 10, Reason: reason}
}
