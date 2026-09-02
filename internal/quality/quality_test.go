package quality

import "testing"

func healthy(upload, download, p99 float64) Metrics {
	return Metrics{UploadGbps: upload, DownloadGbps: download, RTTP99Ms: p99, ReconnectPassed: 5, ReconnectTotal: 5, IntegrityUpload: true, IntegrityDown: true}
}

func TestClassifyUsesWeakerDirection(t *testing.T) {
	p := Classify(healthy(19.8, 9.1, 0.8))
	if p.Class != "good" || !p.Ready {
		t.Fatalf("got %#v", p)
	}
}

func TestClassifyRejectsIntegrityFailure(t *testing.T) {
	m := healthy(20, 20, 0.5)
	m.IntegrityDown = false
	p := Classify(m)
	if p.Ready || p.Class != "degraded" {
		t.Fatalf("got %#v", p)
	}
}

func TestQualityBands(t *testing.T) {
	cases := []struct {
		name string
		m    Metrics
		want string
	}{
		{"excellent", healthy(16, 14, 1.0), "excellent"},
		{"good-asymmetric", healthy(19, 7, 3.0), "good"},
		{"constrained", healthy(2.5, 1.2, 12), "constrained"},
		{"slow", healthy(.8, 10, 1), "degraded"},
		{"high-tail", healthy(20, 20, 30), "degraded"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Classify(tc.m).Class; got != tc.want {
				t.Fatalf("got %s, want %s", got, tc.want)
			}
		})
	}
}
