package config

import (
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	t.Setenv("HTTP_ADDR", "")
	t.Setenv("PPROF_ENABLED", "")
	t.Setenv("GRPC_KEEP_ALIVE_TIME", "")
	t.Setenv("GRPC_KEEP_ALIVE_TIMEOUT", "")
	t.Setenv("GRPC_MAX_RECV_MSG_SIZE", "")
	t.Setenv("IRIS_TIMEOUT", "")
	t.Setenv("MODEL_TIMEOUT", "")
	t.Setenv("MAX_PROMPT_LEN", "")
	t.Setenv("LOG_LEVEL", "")
	t.Setenv("HTTP_READ_TIMEOUT", "")
	t.Setenv("HTTP_WRITE_TIMEOUT", "")
	t.Setenv("HTTP_IDLE_TIMEOUT", "")

	cfg := Load()

	if cfg.HTTPAddr != ":8080" {
		t.Errorf("HTTPAddr = %q, want :8080", cfg.HTTPAddr)
	}
	if cfg.PProfAddr != "localhost:6060" {
		t.Errorf("PProfAddr = %q, want localhost:6060", cfg.PProfAddr)
	}
	if cfg.PProfEnabled {
		t.Error("PProfEnabled = true, want false")
	}
	if cfg.AIServiceAddr != "localhost:50051" {
		t.Errorf("AIServiceAddr = %q, want localhost:50051", cfg.AIServiceAddr)
	}
	if cfg.JaegerEndpoint != "localhost:4317" {
		t.Errorf("JaegerEndpoint = %q, want localhost:4317", cfg.JaegerEndpoint)
	}
	if cfg.LogLevel != "info" {
		t.Errorf("LogLevel = %q, want info", cfg.LogLevel)
	}
	if cfg.HTTPReadTimeout != 10*time.Second {
		t.Errorf("HTTPReadTimeout = %v, want 10s", cfg.HTTPReadTimeout)
	}
	if cfg.HTTPWriteTimeout != 75*time.Second {
		t.Errorf("HTTPWriteTimeout = %v, want 75s", cfg.HTTPWriteTimeout)
	}
	if cfg.HTTPIdleTimeout != 60*time.Second {
		t.Errorf("HTTPIdleTimeout = %v, want 60s", cfg.HTTPIdleTimeout)
	}
	if cfg.GRPCKeepAliveTime != 30*time.Second {
		t.Errorf("GRPCKeepAliveTime = %v, want 30s", cfg.GRPCKeepAliveTime)
	}
	if cfg.GRPCKeepAliveTimeout != 3*time.Second {
		t.Errorf("GRPCKeepAliveTimeout = %v, want 3s", cfg.GRPCKeepAliveTimeout)
	}
	if cfg.GRPCMaxRecvMsgSize != 50*1024*1024 {
		t.Errorf("GRPCMaxRecvMsgSize = %d, want %d", cfg.GRPCMaxRecvMsgSize, 50*1024*1024)
	}
	if cfg.IrisTimeout != 3*time.Second {
		t.Errorf("IrisTimeout = %v, want 3s", cfg.IrisTimeout)
	}
	if cfg.ModelTimeout != 60*time.Second {
		t.Errorf("ModelTimeout = %v, want 60s", cfg.ModelTimeout)
	}
	if cfg.MaxPromptLen != 2000 {
		t.Errorf("MaxPromptLen = %d, want 2000", cfg.MaxPromptLen)
	}
}

func TestLoadEnvOverrides(t *testing.T) {
	t.Setenv("HTTP_ADDR", ":9090")
	t.Setenv("PPROF_ENABLED", "true")
	t.Setenv("GRPC_KEEP_ALIVE_TIME", "15s")
	t.Setenv("GRPC_KEEP_ALIVE_TIMEOUT", "5s")
	t.Setenv("GRPC_MAX_RECV_MSG_SIZE", "1048576")
	t.Setenv("IRIS_TIMEOUT", "7s")
	t.Setenv("MODEL_TIMEOUT", "120s")
	t.Setenv("MAX_PROMPT_LEN", "500")
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("HTTP_READ_TIMEOUT", "20s")
	t.Setenv("HTTP_WRITE_TIMEOUT", "150s")
	t.Setenv("HTTP_IDLE_TIMEOUT", "90s")

	cfg := Load()

	if cfg.HTTPAddr != ":9090" {
		t.Errorf("HTTPAddr = %q, want :9090", cfg.HTTPAddr)
	}
	if !cfg.PProfEnabled {
		t.Error("PProfEnabled = false, want true")
	}
	if cfg.GRPCKeepAliveTime != 15*time.Second {
		t.Errorf("GRPCKeepAliveTime = %v, want 15s", cfg.GRPCKeepAliveTime)
	}
	if cfg.GRPCKeepAliveTimeout != 5*time.Second {
		t.Errorf("GRPCKeepAliveTimeout = %v, want 5s", cfg.GRPCKeepAliveTimeout)
	}
	if cfg.GRPCMaxRecvMsgSize != 1048576 {
		t.Errorf("GRPCMaxRecvMsgSize = %d, want 1048576", cfg.GRPCMaxRecvMsgSize)
	}
	if cfg.IrisTimeout != 7*time.Second {
		t.Errorf("IrisTimeout = %v, want 7s", cfg.IrisTimeout)
	}
	if cfg.ModelTimeout != 120*time.Second {
		t.Errorf("ModelTimeout = %v, want 120s", cfg.ModelTimeout)
	}
	if cfg.MaxPromptLen != 500 {
		t.Errorf("MaxPromptLen = %d, want 500", cfg.MaxPromptLen)
	}
	if cfg.LogLevel != "debug" {
		t.Errorf("LogLevel = %q, want debug", cfg.LogLevel)
	}
	if cfg.HTTPReadTimeout != 20*time.Second {
		t.Errorf("HTTPReadTimeout = %v, want 20s", cfg.HTTPReadTimeout)
	}
	if cfg.HTTPWriteTimeout != 150*time.Second {
		t.Errorf("HTTPWriteTimeout = %v, want 150s", cfg.HTTPWriteTimeout)
	}
	if cfg.HTTPIdleTimeout != 90*time.Second {
		t.Errorf("HTTPIdleTimeout = %v, want 90s", cfg.HTTPIdleTimeout)
	}
}

func TestWriteTimeoutRaisedToCoverModelTimeout(t *testing.T) {
	t.Setenv("MODEL_TIMEOUT", "60s")
	t.Setenv("HTTP_WRITE_TIMEOUT", "10s")

	cfg := Load()

	want := 60*time.Second + writeTimeoutHeadroom
	if cfg.HTTPWriteTimeout != want {
		t.Errorf("HTTPWriteTimeout = %v, want %v (raised to cover MODEL_TIMEOUT)", cfg.HTTPWriteTimeout, want)
	}
}

func TestWriteTimeoutDisabledIsKept(t *testing.T) {
	t.Setenv("MODEL_TIMEOUT", "60s")
	t.Setenv("HTTP_WRITE_TIMEOUT", "0s")

	cfg := Load()

	if cfg.HTTPWriteTimeout != 0 {
		t.Errorf("HTTPWriteTimeout = %v, want 0 (disabled)", cfg.HTTPWriteTimeout)
	}
}

func TestGetEnvBool(t *testing.T) {
	tests := []struct {
		value    string
		fallback bool
		want     bool
	}{
		{"true", false, true},
		{"1", false, true},
		{"false", true, false},
		{"0", true, false},
		{"invalid", true, true},
	}

	for _, tt := range tests {
		t.Setenv("TEST_BOOL", tt.value)
		if got := getEnvBool("TEST_BOOL", tt.fallback); got != tt.want {
			t.Errorf("getEnvBool(%q, %v) = %v, want %v", tt.value, tt.fallback, got, tt.want)
		}
	}
}
