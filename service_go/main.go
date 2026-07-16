// Command go-gateway is the REST-to-gRPC API gateway process.
//
// Startup order: config → logging → optional pprof → metrics → tracing →
// gRPC dial (+ Connect to leave Idle) → handlers/router → HTTP listen →
// SIGTERM/SIGINT graceful Shutdown with cfg.ShutdownTimeout.
package main

import (
	"context"
	"log/slog"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"my-go-gateway/config"
	irisv1 "my-go-gateway/gen/iris/v1"
	modelv1 "my-go-gateway/gen/model/v1"
	"my-go-gateway/grpcclient"
	"my-go-gateway/handlers"
	"my-go-gateway/metrics"
	"my-go-gateway/router"
	"my-go-gateway/telemetry"
)

func main() {
	cfg := config.Load()

	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: parseLogLevel(cfg.LogLevel),
	})))

	if cfg.PProfEnabled {
		go func() {
			slog.Info("pprof running", "addr", "http://localhost"+cfg.PProfAddr+"/debug/pprof")
			if err := http.ListenAndServe(cfg.PProfAddr, nil); err != nil {
				slog.Error("pprof failed", "error", err)
			}
		}()
	}

	metrics.Register()

	tp, err := telemetry.InitTracer(context.Background(), cfg.JaegerEndpoint)
	if err != nil {
		slog.Error("failed to initialize tracer", "error", err)
		os.Exit(1)
	}
	defer func() { _ = tp.Shutdown(context.Background()) }()

	conn, err := grpcclient.Dial(cfg)
	if err != nil {
		slog.Error("failed to connect to AI service", "error", err)
		os.Exit(1)
	}
	defer conn.Close()
	// grpc.NewClient is lazy — kick the dial at startup so the channel moves
	// out of Idle before the first /readyz probe (Readyz still requires Ready).
	conn.Connect()

	healthHandler := handlers.NewHealthHandler(conn)
	irisHandler := handlers.NewIrisHandler(irisv1.NewIrisPredictorClient(conn), cfg)
	modelHandler := handlers.NewModelHandler(modelv1.NewModelPredictorClient(conn), cfg)

	r := router.Setup(healthHandler, irisHandler, modelHandler)

	srv := &http.Server{
		Addr:         cfg.HTTPAddr,
		Handler:      r,
		ReadTimeout:  cfg.HTTPReadTimeout,
		WriteTimeout: cfg.HTTPWriteTimeout,
		IdleTimeout:  cfg.HTTPIdleTimeout,
	}

	go func() {
		slog.Info("Go Gateway running", "addr", cfg.HTTPAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("HTTP server error", "error", err)
			os.Exit(1)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	slog.Info("Shutting down gracefully...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("forced shutdown", "error", err)
	}
	slog.Info("Server stopped.")
}

// parseLogLevel maps cfg.LogLevel strings to slog levels; unknown values → Info.
func parseLogLevel(level string) slog.Level {
	switch strings.ToLower(level) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
