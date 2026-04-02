// Package metrics provides optional Prometheus metrics for gocacheprog.
package metrics

import (
	"net/http"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics holds all gocacheprog Prometheus metrics.
type Metrics struct {
	RequestsTotal        *prometheus.CounterVec
	CacheHitsTotal       *prometheus.CounterVec
	CacheMissesTotal     prometheus.Counter
	CASUploadBytes       prometheus.Counter
	CASDownloadBytes     prometheus.Counter
	CASDuration          *prometheus.HistogramVec
	ACDuration           *prometheus.HistogramVec
	LocalCacheSizeBytes  prometheus.Gauge
	LocalCacheEntries    prometheus.Gauge
	LocalCacheEvictions  prometheus.Counter
	WorkerPoolActive     prometheus.Gauge
	GRPCConnected        prometheus.Gauge
}

var (
	instance *Metrics
	once     sync.Once
)

// Get returns the singleton Metrics instance.
func Get() *Metrics {
	once.Do(func() {
		instance = newMetrics()
	})
	return instance
}

func newMetrics() *Metrics {
	m := &Metrics{
		RequestsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gocacheprog_requests_total",
			Help: "Total requests by command and status",
		}, []string{"command", "status"}),
		CacheHitsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gocacheprog_cache_hits_total",
			Help: "Cache hits by tier",
		}, []string{"tier"}),
		CacheMissesTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "gocacheprog_cache_misses_total",
			Help: "Total cache misses",
		}),
		CASUploadBytes: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "gocacheprog_cas_upload_bytes_total",
			Help: "Total bytes uploaded to CAS",
		}),
		CASDownloadBytes: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "gocacheprog_cas_download_bytes_total",
			Help: "Total bytes downloaded from CAS",
		}),
		CASDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "gocacheprog_cas_operation_duration_seconds",
			Help:    "CAS operation duration",
			Buckets: prometheus.DefBuckets,
		}, []string{"operation"}),
		ACDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "gocacheprog_ac_operation_duration_seconds",
			Help:    "AC operation duration",
			Buckets: prometheus.DefBuckets,
		}, []string{"operation"}),
		LocalCacheSizeBytes: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "gocacheprog_local_cache_size_bytes",
			Help: "Current local cache size in bytes",
		}),
		LocalCacheEntries: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "gocacheprog_local_cache_entries",
			Help: "Number of local cache entries",
		}),
		LocalCacheEvictions: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "gocacheprog_local_cache_evictions_total",
			Help: "Total local cache evictions",
		}),
		WorkerPoolActive: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "gocacheprog_worker_pool_active",
			Help: "Number of active workers",
		}),
		GRPCConnected: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "gocacheprog_grpc_connected",
			Help: "Whether gRPC is connected (1=yes, 0=no)",
		}),
	}

	prometheus.MustRegister(
		m.RequestsTotal,
		m.CacheHitsTotal,
		m.CacheMissesTotal,
		m.CASUploadBytes,
		m.CASDownloadBytes,
		m.CASDuration,
		m.ACDuration,
		m.LocalCacheSizeBytes,
		m.LocalCacheEntries,
		m.LocalCacheEvictions,
		m.WorkerPoolActive,
		m.GRPCConnected,
	)

	return m
}

// Serve starts an HTTP server for Prometheus metrics on the given address.
func Serve(addr string) error {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	return http.ListenAndServe(addr, mux)
}
