package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"runtime/pprof"
	"syscall"

	"github.com/hacktohell/gocache-rbe/internal/cache"
	"github.com/hacktohell/gocache-rbe/internal/config"
	"github.com/hacktohell/gocache-rbe/internal/handler"
	"github.com/hacktohell/gocache-rbe/internal/protocol"
	"github.com/hacktohell/gocache-rbe/internal/reapi"
	"golang.org/x/sync/semaphore"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if err := run(ctx); err != nil {
		slog.Error("fatal", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	if profilePath := os.Getenv("GOCACHE_RBE_CPUPROFILE"); profilePath != "" {
		f, err := os.Create(profilePath)
		if err != nil {
			return fmt.Errorf("create cpu profile: %w", err)
		}
		if err := pprof.StartCPUProfile(f); err != nil {
			f.Close()
			return fmt.Errorf("start cpu profile: %w", err)
		}
		defer func() { pprof.StopCPUProfile(); f.Close() }()
		slog.Info("cpu profiling enabled", "path", profilePath)
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	setupLogging(cfg.LogLevel)

	dc, err := cache.NewDiskCache(cfg.CacheDir, cfg.CacheSizeBytes())
	if err != nil {
		return err
	}

	var client *reapi.Client
	client, err = reapi.NewClient(ctx, reapi.ClientConfig{
		Target:         cfg.Target,
		InstanceName:   cfg.InstanceName,
		TLSCert:        cfg.TLSCert,
		TLSKey:         cfg.TLSKey,
		TLSCA:          cfg.TLSCA,
		ConnectTimeout: cfg.ConnectTimeout.Duration,
		RequestTimeout: cfg.RequestTimeout.Duration,
		TLS:            cfg.TLS,
		AuthHeader:     cfg.AuthHeader,
		AuthToken:      cfg.AuthToken,
	})
	if err != nil {
		slog.Warn("remote unavailable, starting in local-only mode", "error", err)
		client = nil
	}

	h := &handler.Handler{
		Cache:           dc,
		Client:          client,
		RemoteSem:       semaphore.NewWeighted(int64(cfg.Workers * 4)),
		MaxArtifactSize: cfg.MaxArtifactSizeBytes(),
	}

	if cfg.HealthAddr != "" {
		go func() {
			mux := http.NewServeMux()
			mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("ok"))
			})
			mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("ok"))
			})
			slog.Info("health server starting", "addr", cfg.HealthAddr)
			if err := http.ListenAndServe(cfg.HealthAddr, mux); err != nil {
				slog.Error("health server failed", "error", err)
			}
		}()
	}

	reader := protocol.NewReader(os.Stdin)
	writer := protocol.NewWriter(os.Stdout)

	h.Run(ctx, reader, writer, cfg.Workers)
	return nil
}

func setupLogging(level string) {
	var lvl slog.Level
	switch level {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: lvl}))
	slog.SetDefault(logger)
}
