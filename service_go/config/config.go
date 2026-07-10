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

	GRPCKeepAliveTime    time.Duration
	GRPCKeepAliveTimeout time.Duration
	GRPCMaxRecvMsgSize   int

	IrisTimeout  time.Duration
	ModelTimeout time.Duration
	MaxPromptLen int
}

func Load() *Config {
	return &Config{
		HTTPAddr:       getEnv("HTTP_ADDR", ":8080"),
		PProfAddr:      getEnv("PPROF_ADDR", ":6060"),
		PProfEnabled:   getEnvBool("PPROF_ENABLED", false),
		AIServiceAddr:  getEnv("AI_SERVICE_ADDR", "localhost:50051"),
		JaegerEndpoint: getEnv("JAEGER_ENDPOINT", "localhost:4317"),
		LogLevel:       getEnv("LOG_LEVEL", "info"),

		HTTPReadTimeout:  getEnvDuration("HTTP_READ_TIMEOUT", 10*time.Second),
		HTTPWriteTimeout: getEnvDuration("HTTP_WRITE_TIMEOUT", 10*time.Second),
		HTTPIdleTimeout:  getEnvDuration("HTTP_IDLE_TIMEOUT", 60*time.Second),

		GRPCKeepAliveTime:    getEnvDuration("GRPC_KEEP_ALIVE_TIME", 10*time.Second),
		GRPCKeepAliveTimeout: getEnvDuration("GRPC_KEEP_ALIVE_TIMEOUT", 3*time.Second),
		GRPCMaxRecvMsgSize:   getEnvInt("GRPC_MAX_RECV_MSG_SIZE", 50*1024*1024),

		IrisTimeout:  getEnvDuration("IRIS_TIMEOUT", 3*time.Second),
		ModelTimeout: getEnvDuration("MODEL_TIMEOUT", 60*time.Second),
		MaxPromptLen: getEnvInt("MAX_PROMPT_LEN", 2000),
	}
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
