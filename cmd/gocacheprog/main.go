package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/hacktohell/rbe_gocacheprog/internal/cache"
	"github.com/hacktohell/rbe_gocacheprog/internal/config"
	"github.com/hacktohell/rbe_gocacheprog/internal/handler"
	"github.com/hacktohell/rbe_gocacheprog/internal/metrics"
	"github.com/hacktohell/rbe_gocacheprog/internal/protocol"
	"github.com/hacktohell/rbe_gocacheprog/internal/reapi"
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
		ConnectTimeout: cfg.ConnectTimeout,
		RequestTimeout: cfg.RequestTimeout,
	})
	if err != nil {
		slog.Warn("remote unavailable, starting in local-only mode", "error", err)
		client = nil
	}

	if cfg.MetricsAddr != "" {
		metrics.Get() // register application metrics with the global Prometheus registry
		go func() {
			slog.Info("metrics server starting", "addr", cfg.MetricsAddr)
			if err := metrics.Serve(cfg.MetricsAddr); err != nil {
				slog.Error("metrics server failed", "error", err)
			}
		}()
	}

	h := &handler.Handler{
		Cache:  dc,
		Client: client,
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
