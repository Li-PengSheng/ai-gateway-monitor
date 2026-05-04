package main

import (
	"context"
	"log/slog"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
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

	// pprof on a separate port
	go func() {
		slog.Info("pprof running", "addr", "http://localhost"+cfg.PProfAddr+"/debug/pprof")
		if err := http.ListenAndServe(cfg.PProfAddr, nil); err != nil {
			slog.Error("pprof failed", "error", err)
		}
	}()

	// Metrics
	metrics.Register()

	// Tracing
	tp, err := telemetry.InitTracer(context.Background(), cfg.JaegerEndpoint)
	if err != nil {
		slog.Error("failed to initialize tracer", "error", err)
		os.Exit(1)
	}
	defer func() { _ = tp.Shutdown(context.Background()) }()

	// gRPC connection
	conn, err := grpcclient.Dial(cfg)
	if err != nil {
		slog.Error("failed to connect to AI service", "error", err)
		os.Exit(1)
	}
	defer conn.Close()

	// Handlers
	healthHandler := handlers.NewHealthHandler()
	irisHandler := handlers.NewIrisHandler(irisv1.NewIrisPredictorClient(conn), cfg)
	modelHandler := handlers.NewModelHandler(modelv1.NewModelPredictorClient(conn), cfg)

	// Router
	r := router.Setup(healthHandler, irisHandler, modelHandler)

	srv := &http.Server{Addr: cfg.HTTPAddr, Handler: r}

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
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.GRPCKeepAliveTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("forced shutdown", "error", err)
	}
	slog.Info("Server stopped.")
}
