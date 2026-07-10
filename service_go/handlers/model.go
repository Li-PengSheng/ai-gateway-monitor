// service_go/handlers/model.go
package handlers

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"

	"my-go-gateway/config"
	modelv1 "my-go-gateway/gen/model/v1"
	"my-go-gateway/metrics"
)

type ModelHandler struct {
	client modelv1.ModelPredictorClient
	cfg    *config.Config
}

func NewModelHandler(client modelv1.ModelPredictorClient, cfg *config.Config) *ModelHandler {
	return &ModelHandler{client: client, cfg: cfg}
}

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
	if len(body.Prompt) > h.cfg.MaxPromptLen {
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

func (h *ModelHandler) PredictStream(c *gin.Context) {
	// 1. HTTP duration timer (same as Predict)
	timer := prometheus.NewTimer(metrics.HTTPDuration.WithLabelValues("/predict/model/stream"))
	defer timer.ObserveDuration()

	var body struct {
		Prompt string `json:"prompt" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		writeValidationError(c, "/predict/model/stream", err.Error())
		return
	}
	if len(body.Prompt) > h.cfg.MaxPromptLen {
		writeValidationError(c, "/predict/model/stream",
			fmt.Sprintf("prompt exceeds maximum length of %d characters", h.cfg.MaxPromptLen))
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), h.cfg.ModelTimeout)
	defer cancel()

	// 2. gRPC stream initiation duration
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

	// Set SSE headers
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no") // disable Nginx buffering

	// 3. Count the stream as 200 once we start sending
	metrics.HTTPRequestsTotal.WithLabelValues("/predict/model/stream", "200").Inc()

	c.Stream(func(w io.Writer) bool {
		chunk, err := stream.Recv()
		if err == io.EOF {
			// Send a final done event
			c.SSEvent("done", gin.H{"done": true})
			return false // stop streaming
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

		// Record metrics on the final chunk (when eval_count is populated)
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
		return true // continue streaming
	})
}
