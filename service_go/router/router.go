package router

import (
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"my-go-gateway/handlers"
)

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
