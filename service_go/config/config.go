// service_go/config/config.go

package config

import (
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	HTTPAddr       string
	PProfAddr      string
	PProfEnabled   bool
	AIServiceAddr  string
	JaegerEndpoint string
	LogLevel       string

	HTTPReadTimeout  time.Duration
	HTTPWriteTimeout time.Duration
	HTTPIdleTimeout  time.Duration
	ShutdownTimeout  time.Duration

	GRPCKeepAliveTime    time.Duration
	GRPCKeepAliveTimeout time.Duration
	GRPCMaxRecvMsgSize   int

	IrisTimeout  time.Duration
	ModelTimeout time.Duration
	MaxPromptLen int
}

// writeTimeoutHeadroom is added on top of ModelTimeout so the handler can
// still flush a well-formed timeout response before the connection deadline.
const writeTimeoutHeadroom = 15 * time.Second

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

	// http.Server.WriteTimeout is an absolute deadline for the whole response.
	// If it is shorter than ModelTimeout, every LLM generation (or SSE stream)
	// that outlives it gets its connection killed mid-response.
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
