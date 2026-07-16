package handlers

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"

	"my-go-gateway/config"
	irisv1 "my-go-gateway/gen/iris/v1"
	"my-go-gateway/metrics"
)

// maxIrisFeatureCm bounds feature values: iris measurements are a few cm, so
// anything beyond this is a client error, not a legitimate flower.
const maxIrisFeatureCm = 50

// validateIrisFeature rejects non-finite values and measurements outside the
// inclusive [0, maxIrisFeatureCm] range. The gateway validates before making an
// RPC to return a stable HTTP 400 cheaply; python-ai repeats the check because
// gRPC callers may bypass the gateway. It returns nil for a valid measurement
// and has no side effects.
func validateIrisFeature(name string, v float32) error {
	f := float64(v)
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return fmt.Errorf("%s must be a finite number", name)
	}
	if f < 0 || f > maxIrisFeatureCm {
		return fmt.Errorf("%s must be between 0 and %d", name, maxIrisFeatureCm)
	}
	return nil
}

// IrisHandler serves POST /predict/iris by validating JSON features and
// forwarding an IrisPredict RPC to python-ai.
type IrisHandler struct {
	client  irisv1.IrisPredictorClient
	cfg     *config.Config
	reqPool sync.Pool
}

// NewIrisHandler returns an IrisHandler. reqPool reuses the small request proto
// because this endpoint is the high-throughput, non-LLM path and otherwise
// allocates the same short-lived object for every call. sync.Pool is only a
// best-effort cache: the runtime may discard entries at any garbage collection.
func NewIrisHandler(client irisv1.IrisPredictorClient, cfg *config.Config) *IrisHandler {
	return &IrisHandler{
		client: client,
		cfg:    cfg,
		reqPool: sync.Pool{
			New: func() interface{} {
				return new(irisv1.IrisPredictRequest)
			},
		},
	}
}

// Predict handles POST /predict/iris.
//
// Request JSON (all four floats required; zero is valid):
//
//	{"sepal_length":n,"sepal_width":n,"petal_length":n,"petal_width":n}
//
// Success 200: {"result":className,"id":classId,"source":"..."}.
// Validation failures → 400 INVALID_ARGUMENT; gRPC failures via writeGRPCError.
//
// Side effects: Prometheus HTTP/gRPC metrics; uses cfg.IrisTimeout as RPC deadline.
//
// Why pointer fields: binding:"required" on float32 rejects legitimate 0.0
// measurements; pointers distinguish "key absent" from "value is 0".
func (h *IrisHandler) Predict(c *gin.Context) {
	timer := prometheus.NewTimer(metrics.HTTPDuration.WithLabelValues("/predict/iris"))
	defer timer.ObserveDuration()

	var body struct {
		SepalLength *float32 `json:"sepal_length"`
		SepalWidth  *float32 `json:"sepal_width"`
		PetalLength *float32 `json:"petal_length"`
		PetalWidth  *float32 `json:"petal_width"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		writeValidationError(c, "/predict/iris", err.Error())
		return
	}
	if body.SepalLength == nil || body.SepalWidth == nil ||
		body.PetalLength == nil || body.PetalWidth == nil {
		writeValidationError(c, "/predict/iris",
			"all iris features are required: sepal_length, sepal_width, petal_length, petal_width")
		return
	}
	for name, v := range map[string]float32{
		"sepal_length": *body.SepalLength,
		"sepal_width":  *body.SepalWidth,
		"petal_length": *body.PetalLength,
		"petal_width":  *body.PetalWidth,
	} {
		if err := validateIrisFeature(name, v); err != nil {
			writeValidationError(c, "/predict/iris", err.Error())
			return
		}
	}

	// Borrow a request proto from the pool; Reset before Put so pooled objects
	// never leak previous feature values across requests.
	req := h.reqPool.Get().(*irisv1.IrisPredictRequest)
	defer func() {
		req.Reset()
		h.reqPool.Put(req)
	}()

	req.SepalLength = *body.SepalLength
	req.SepalWidth = *body.SepalWidth
	req.PetalLength = *body.PetalLength
	req.PetalWidth = *body.PetalWidth

	ctx, cancel := context.WithTimeout(c.Request.Context(), h.cfg.IrisTimeout)
	defer cancel()

	grpcStart := time.Now()
	resp, err := h.client.IrisPredict(ctx, req)
	grpcStatus := "ok"
	if err != nil {
		grpcStatus = "error"
	}
	metrics.GRPCRequestDuration.WithLabelValues("IrisPredict", grpcStatus).
		Observe(time.Since(grpcStart).Seconds())

	if err != nil {
		slog.Error("iris service call failed", "error", err)
		writeGRPCError(c, "/predict/iris", err)
		return
	}

	metrics.HTTPRequestsTotal.WithLabelValues("/predict/iris", "200").Inc()
	c.JSON(http.StatusOK, gin.H{
		"result": resp.ClassName,
		"id":     resp.ClassId,
		"source": "Go Gateway -> Python AI (Iris)",
	})
}
