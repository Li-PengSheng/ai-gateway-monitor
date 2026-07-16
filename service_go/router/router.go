// Package router wires Gin routes for go-gateway probes, metrics, and predict APIs.
package router

import (
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"my-go-gateway/handlers"
)

// Setup builds the Gin engine and registers the public HTTP surface:
//
//	GET  /metrics
//	GET  /healthz
//	GET  /readyz
//	POST /predict/iris
//	POST /predict/model
//	POST /predict/model/stream
//
// Parameters: non-nil health, iris, and model handlers. Returns a ready-to-serve
// *gin.Engine (gin.Default middleware: Logger + Recovery).
//
// Side effects: none beyond constructing the engine; does not listen.
func Setup(
	health *handlers.HealthHandler,
	iris *handlers.IrisHandler,
	model *handlers.ModelHandler,
) *gin.Engine {
	r := gin.Default()

	r.GET("/metrics", gin.WrapH(promhttp.Handler()))
	r.GET("/healthz", health.Check)
	r.GET("/readyz", health.Readyz)
	r.POST("/predict/iris", iris.Predict)
	r.POST("/predict/model", model.Predict)
	r.POST("/predict/model/stream", model.PredictStream)
	return r
}
