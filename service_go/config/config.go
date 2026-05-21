// service_go/config/config.go

package config

import (
	"os"
	"time"
)

type Config struct {
	HTTPAddr       string
	PProfAddr      string
	AIServiceAddr  string
	JaegerEndpoint string

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
		AIServiceAddr:  getEnv("AI_SERVICE_ADDR", "localhost:50051"),
		JaegerEndpoint: getEnv("JAEGER_ENDPOINT", "localhost:4317"),

		GRPCKeepAliveTime:    10 * time.Second,
		GRPCKeepAliveTimeout: 3 * time.Second,
		GRPCMaxRecvMsgSize:   50 * 1024 * 1024, // 50 MB

		IrisTimeout:  3 * time.Second,
		ModelTimeout: 60 * time.Second,
		MaxPromptLen: 2000,
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
