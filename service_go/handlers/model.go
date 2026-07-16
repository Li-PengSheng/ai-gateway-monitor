package handlers

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"

	"my-go-gateway/config"
	modelv1 "my-go-gateway/gen/model/v1"
	"my-go-gateway/metrics"
)

// ModelHandler serves POST /predict/model (unary) and /predict/model/stream (SSE)
// by forwarding ModelPredict / ModelPredictStream RPCs to python-ai.
type ModelHandler struct {
	client modelv1.ModelPredictorClient
	cfg    *config.Config
}

// NewModelHandler returns a ModelHandler using cfg.ModelTimeout and MaxPromptLen.
func NewModelHandler(client modelv1.ModelPredictorClient, cfg *config.Config) *ModelHandler {
	return &ModelHandler{client: client, cfg: cfg}
}

// Predict handles POST /predict/model (unary JSON response).
//
// Request JSON: {"prompt":"..."}. Prompt length is counted in Unicode runes
// against cfg.MaxPromptLen (not bytes).
//
// Success 200 includes reply, model name, and token/duration metrics.
// Validation → 400; gRPC errors via writeGRPCError (504/503/400/500).
//
// Side effects: Prometheus HTTP/gRPC/AI metrics; RPC deadline = cfg.ModelTimeout.
func (h *ModelHandler) Predict(c *gin.Context) {
	timer := prometheus.NewTimer(metrics.HTTPDuration.WithLabelValues("/predict/model"))
	defer timer.ObserveDuration()

	var body struct {
		Prompt string `json:"prompt" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		writeValidationError(c, "/predict/model", err.Error())
		return
	}
	// Count runes, not bytes: with len() a Chinese prompt would hit the limit
	// at roughly a third of the advertised character budget.
	if utf8.RuneCountInString(body.Prompt) > h.cfg.MaxPromptLen {
		writeValidationError(c, "/predict/model",
			fmt.Sprintf("prompt exceeds maximum length of %d characters", h.cfg.MaxPromptLen))
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), h.cfg.ModelTimeout)
	defer cancel()

	grpcStart := time.Now()
	resp, err := h.client.ModelPredict(ctx, &modelv1.ModelPredictRequest{
		Prompt: body.Prompt,
	})
	grpcStatus := "ok"
	if err != nil {
		grpcStatus = "error"
	}
	metrics.GRPCRequestDuration.WithLabelValues("ModelPredict", grpcStatus).
		Observe(time.Since(grpcStart).Seconds())

	if err != nil {
		slog.Error("model service call failed", "error", err)
		writeGRPCError(c, "/predict/model", err)
		return
	}

	if resp.EvalCount > 0 {
		metrics.AITokensTotal.WithLabelValues(resp.ModelName).Add(float64(resp.EvalCount))
		metrics.AIGenerationDuration.WithLabelValues(resp.ModelName).
			Observe(float64(resp.EvalDuration) / 1e9)
	}

	metrics.HTTPRequestsTotal.WithLabelValues("/predict/model", "200").Inc()
	c.JSON(http.StatusOK, gin.H{
		"reply": resp.Response,
		"model": resp.ModelName,
		"metrics": gin.H{
			"prompt_tokens": resp.PromptEvalCount,
			"output_tokens": resp.EvalCount,
			"duration_sec":  float64(resp.EvalDuration) / 1e9,
		},
		"source": "Go Gateway -> Ollama (Qwen)",
	})
}

// PredictStream handles POST /predict/model/stream as Server-Sent Events.
//
// Request JSON: {"prompt":"..."} (same validation as Predict).
//
// SSE event types after the gRPC client stream is created:
//   - event: message — token chunk payload (reply/model/metrics)
//   - event: done    — stream completed cleanly ({"done":true})
//   - event: error   — any Recv failure (including backend rejection before the
//     first chunk) uses code=STREAM_ERROR; HTTP status stays 200
//
// Why count HTTP 200 before the first Recv: the SSE headers are committed before
// consuming the stream, and gRPC server status commonly arrives on Recv rather
// than during client-stream creation. Those failures cannot change the HTTP
// status, so clients must inspect event:error. Only client-side stream creation
// failures use writeGRPCError and a non-200 JSON response.
//
// X-Accel-Buffering: no disables nginx proxy buffering so tokens flush promptly.
//
// Side effects: Prometheus metrics; RPC deadline = cfg.ModelTimeout.
func (h *ModelHandler) PredictStream(c *gin.Context) {
	timer := prometheus.NewTimer(metrics.HTTPDuration.WithLabelValues("/predict/model/stream"))
	defer timer.ObserveDuration()

	var body struct {
		Prompt string `json:"prompt" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		writeValidationError(c, "/predict/model/stream", err.Error())
		return
	}
	if utf8.RuneCountInString(body.Prompt) > h.cfg.MaxPromptLen {
		writeValidationError(c, "/predict/model/stream",
			fmt.Sprintf("prompt exceeds maximum length of %d characters", h.cfg.MaxPromptLen))
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), h.cfg.ModelTimeout)
	defer cancel()

	// Measure stream setup separately from end-to-end delivery. The generated
	// client returns after creating the stream, while HTTPDuration remains active
	// until Gin finishes the SSE response and represents the full stream lifetime.
	grpcStart := time.Now()
	stream, err := h.client.ModelPredictStream(ctx, &modelv1.ModelPredictRequest{
		Prompt: body.Prompt,
	})
	grpcStatus := "ok"
	if err != nil {
		grpcStatus = "error"
	}
	metrics.GRPCRequestDuration.WithLabelValues("ModelPredictStream", grpcStatus).
		Observe(time.Since(grpcStart).Seconds())
	if err != nil {
		slog.Error("model stream initiation failed", "error", err)
		writeGRPCError(c, "/predict/model/stream", err)
		return
	}

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	metrics.HTTPRequestsTotal.WithLabelValues("/predict/model/stream", "200").Inc()

	c.Stream(func(w io.Writer) bool {
		chunk, err := stream.Recv()
		if err == io.EOF {
			c.SSEvent("done", gin.H{"done": true})
			return false
		}
		if err != nil {
			slog.Error("stream recv error", "error", err)
			c.SSEvent("error", gin.H{
				"code":       CodeStreamError,
				"message":    err.Error(),
				"request_id": requestID(c),
			})
			return false
		}

		// Only the final Ollama chunk typically has eval_count populated.
		if chunk.EvalCount > 0 {
			metrics.AITokensTotal.WithLabelValues(chunk.ModelName).Add(float64(chunk.EvalCount))
			metrics.AIGenerationDuration.WithLabelValues(chunk.ModelName).
				Observe(float64(chunk.EvalDuration) / 1e9)
		}

		c.SSEvent("message", gin.H{
			"reply": chunk.Response,
			"model": chunk.ModelName,
			"metrics": gin.H{
				"prompt_tokens": chunk.PromptEvalCount,
				"output_tokens": chunk.EvalCount,
				"duration_sec":  float64(chunk.EvalDuration) / 1e9,
			},
		})
		return true
	})
}
