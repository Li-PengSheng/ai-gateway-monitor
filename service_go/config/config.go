// Package config loads go-gateway runtime settings from the environment and
// enforces cross-field timeout invariants that callers must not violate.
package config

import (
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds process-wide settings for the HTTP server, gRPC client, and
// per-route RPC deadlines. All fields are populated by Load from env vars
// (or defaults); treat the returned value as read-only after startup.
type Config struct {
	// HTTPAddr is the gateway listen address (e.g. ":8080").
	HTTPAddr string
	// PProfAddr is the pprof listen address; default is localhost-only.
	PProfAddr string
	// PProfEnabled turns on the separate pprof HTTP server when true.
	PProfEnabled bool
	// AIServiceAddr is the python-ai gRPC target (host:port).
	AIServiceAddr string
	// JaegerEndpoint is the OTLP gRPC endpoint for trace export.
	JaegerEndpoint string
	// LogLevel is the slog level name (debug/info/warn/error).
	LogLevel string

	// HTTPReadTimeout limits reading the request headers and body; zero disables it.
	HTTPReadTimeout time.Duration
	// HTTPWriteTimeout is an absolute deadline for the entire HTTP response.
	// Load raises it to at least ModelTimeout+15s unless explicitly set to 0
	// (disabled). Must stay above ModelTimeout or LLM/SSE responses are cut off.
	HTTPWriteTimeout time.Duration
	// HTTPIdleTimeout limits how long keep-alive connections remain idle; zero disables it.
	HTTPIdleTimeout time.Duration
	// ShutdownTimeout is the SIGTERM grace period for in-flight HTTP requests.
	// Should cover ModelTimeout and stay under the pod terminationGracePeriodSeconds.
	ShutdownTimeout time.Duration

	// GRPCKeepAliveTime is the client ping interval. Must exceed the python-ai
	// server's grpc.http2.min_ping_interval_without_data_ms (20s) or the
	// server answers with GOAWAY "too_many_pings".
	GRPCKeepAliveTime time.Duration
	// GRPCKeepAliveTimeout limits how long a keepalive ping may wait for its acknowledgement.
	GRPCKeepAliveTimeout time.Duration
	// GRPCMaxRecvMsgSize is the maximum inbound gRPC response size in bytes.
	GRPCMaxRecvMsgSize int

	// IrisTimeout is the per-request deadline for /predict/iris → IrisPredict.
	IrisTimeout time.Duration
	// ModelTimeout is the per-request deadline for /predict/model and stream.
	ModelTimeout time.Duration
	// MaxPromptLen is the max prompt length in Unicode code points (runes).
	MaxPromptLen int
}

// writeTimeoutHeadroom is added on top of ModelTimeout so the handler can
// still flush a well-formed timeout response before the connection deadline.
const writeTimeoutHeadroom = 15 * time.Second

// Load reads environment variables into a Config and applies startup guards.
//
// Returns a non-nil *Config. Invalid env values fall back to defaults with a
// Warn log; they do not panic.
//
// Side effects: may log Warn when env values are invalid or when
// HTTP_WRITE_TIMEOUT is raised to cover ModelTimeout.
//
// Why the write-timeout guard exists: http.Server.WriteTimeout starts when
// headers are read and covers the whole response. A value shorter than
// ModelTimeout silently kills long generations with a connection reset
// instead of a structured APIError.
func Load() *Config {
	cfg := &Config{
		HTTPAddr: getEnv("HTTP_ADDR", ":8080"),
		// localhost-only by default: pprof exposes heap/goroutine dumps and
		// must not be reachable from outside the pod/host unless opted in.
		PProfAddr:      getEnv("PPROF_ADDR", "localhost:6060"),
		PProfEnabled:   getEnvBool("PPROF_ENABLED", false),
		AIServiceAddr:  getEnv("AI_SERVICE_ADDR", "localhost:50051"),
		JaegerEndpoint: getEnv("JAEGER_ENDPOINT", "localhost:4317"),
		LogLevel:       getEnv("LOG_LEVEL", "info"),

		HTTPReadTimeout:  getEnvDuration("HTTP_READ_TIMEOUT", 10*time.Second),
		HTTPWriteTimeout: getEnvDuration("HTTP_WRITE_TIMEOUT", 75*time.Second),
		HTTPIdleTimeout:  getEnvDuration("HTTP_IDLE_TIMEOUT", 60*time.Second),
		// Grace period for in-flight requests on SIGTERM; should cover
		// MODEL_TIMEOUT and stay under the pod's terminationGracePeriodSeconds.
		ShutdownTimeout: getEnvDuration("SHUTDOWN_TIMEOUT", 75*time.Second),

		// Must stay above the python-ai server's
		// grpc.http2.min_ping_interval_without_data_ms (20s), otherwise the
		// server answers keepalive pings with GOAWAY "too_many_pings".
		GRPCKeepAliveTime:    getEnvDuration("GRPC_KEEP_ALIVE_TIME", 30*time.Second),
		GRPCKeepAliveTimeout: getEnvDuration("GRPC_KEEP_ALIVE_TIMEOUT", 3*time.Second),
		GRPCMaxRecvMsgSize:   getEnvInt("GRPC_MAX_RECV_MSG_SIZE", 50*1024*1024),

		IrisTimeout:  getEnvDuration("IRIS_TIMEOUT", 3*time.Second),
		ModelTimeout: getEnvDuration("MODEL_TIMEOUT", 60*time.Second),
		MaxPromptLen: getEnvInt("MAX_PROMPT_LEN", 2000),
	}

	if minWrite := cfg.ModelTimeout + writeTimeoutHeadroom; cfg.HTTPWriteTimeout > 0 && cfg.HTTPWriteTimeout < minWrite {
		slog.Warn("HTTP_WRITE_TIMEOUT shorter than MODEL_TIMEOUT would cut off model responses; raising it",
			"configured", cfg.HTTPWriteTimeout.String(), "effective", minWrite.String())
		cfg.HTTPWriteTimeout = minWrite
	}

	return cfg
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvDuration(key string, fallback time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		slog.Warn("invalid environment variable, using default",
			"key", key, "value", v, "default", fallback.String())
		return fallback
	}
	return d
}

func getEnvInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		slog.Warn("invalid environment variable, using default",
			"key", key, "value", v, "default", fallback)
		return fallback
	}
	return n
}

func getEnvBool(key string, fallback bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	switch strings.ToLower(v) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		slog.Warn("invalid environment variable, using default",
			"key", key, "value", v, "default", fallback)
		return fallback
	}
}
