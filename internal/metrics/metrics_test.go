package metrics_test

import (
	"testing"

	"github.com/hacktohell/rbe_gocacheprog/internal/metrics"
)

func TestMetricsRegistration(t *testing.T) {
	m := metrics.Get()
	if m == nil {
		t.Fatal("expected non-nil metrics")
	}

	// Verify counters can be incremented without panic
	m.RequestsTotal.WithLabelValues("get", "hit").Inc()
	m.RequestsTotal.WithLabelValues("put", "ok").Inc()
	m.CacheHitsTotal.WithLabelValues("local").Inc()
	m.CacheHitsTotal.WithLabelValues("remote").Inc()
	m.CacheMissesTotal.Inc()
	m.CASUploadBytes.Add(1024)
	m.CASDownloadBytes.Add(2048)
	m.CASDuration.WithLabelValues("upload").Observe(0.5)
	m.ACDuration.WithLabelValues("get").Observe(0.1)
	m.LocalCacheSizeBytes.Set(1024 * 1024)
	m.LocalCacheEntries.Set(42)
	m.LocalCacheEvictions.Inc()
	m.WorkerPoolActive.Set(4)
	m.GRPCConnected.Set(1)
}

func TestMetricsSingleton(t *testing.T) {
	m1 := metrics.Get()
	m2 := metrics.Get()
	if m1 != m2 {
		t.Error("Get() should return the same instance")
	}
}
