package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	_ "net/http/pprof" //
	// --- 链路追踪相关核心依赖 ---
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.17.0"

	// 拦截器与插件
	irisv1  "my-go-gateway/gen/iris/v1"
	modelv1 "my-go-gateway/gen/model/v1"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
)

var (
	// 记录请求总数 (用于计算 QPS)
	httpRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Total number of HTTP requests.",
		},
		[]string{"path", "status"},
	)
	// 记录请求耗时 (用于计算 P99 延迟)
	httpDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "Histogram of response latency for HTTP requests.",
			Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
		},
		[]string{"path"},
	)
)

func init() {
	// 注册指标
	prometheus.MustRegister(httpRequestsTotal)
	prometheus.MustRegister(httpDuration)
}

// 初始化 OpenTelemetry 追踪器
func initTracer() (*sdktrace.TracerProvider, error) {
	ctx := context.Background()
	// 连接到 Docker Compose 中的 Jaeger 服务
	exporter, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpoint("jaeger:4317"),
		otlptracegrpc.WithInsecure(),
	)
	if err != nil {
		return nil, err
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceNameKey.String("go-gateway"),
		)),
	)

	otel.SetTracerProvider(tp)
	// 关键：设置全局传播器，这样 otelgrpc 才能把 ID 传出去
	otel.SetTextMapPropagator(propagation.TraceContext{})
	return tp, nil
}

func main() {

	go func() {
		// 建议使用独立端口，避免干扰业务逻辑
		slog.Info("pprof running on http://localhost:6060/debug/pprof")
		if err := http.ListenAndServe(":6060", nil); err != nil {
			slog.Error("pprof failed", "error", err)
		}
	}()

	// 1. 初始化链路追踪
	tp, err := initTracer()
	if err != nil {
		slog.Error("failed to initialize tracer", "error", err)
		os.Exit(1)
	}
	defer func() { _ = tp.Shutdown(context.Background()) }()

	addr := os.Getenv("AI_SERVICE_ADDR")
	if addr == "" {
		addr = "localhost:50051" // local dev fallback
	}
	conn, err := grpc.NewClient(
		addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
	)
	if err != nil {
		slog.Error("failed to connect to AI service", "error", err)
		os.Exit(1)
	}
	defer conn.Close()

	irisClient := irisv1.NewIrisPredictorServiceClient(conn)
	modelClient := modelv1.NewModelPredictorServiceClient(conn)

	// 1. 定义全局对象池
	var predictReqPool = sync.Pool{
		New: func() interface{} {
			return new(irisv1.IrisPredictRequest) // 篮子空了才创建新的
		},
	}

	r := gin.Default()

	r.GET("/metrics", gin.WrapH(promhttp.Handler()))

	//iris
	r.POST("/predict/iris", func(c *gin.Context) {
		timer := prometheus.NewTimer(httpDuration.WithLabelValues("/predict/iris"))
		defer timer.ObserveDuration()

		// ✓ bind into a plain struct first — proto structs don't have json tags
		var body struct {
			SepalLength float32 `json:"sepal_length"`
			SepalWidth  float32 `json:"sepal_width"`
			PetalLength float32 `json:"petal_length"`
			PetalWidth  float32 `json:"petal_width"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			httpRequestsTotal.WithLabelValues("/predict/iris", "400").Inc()
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		// ✓ correct type assertion
		req := predictReqPool.Get().(*irisv1.IrisPredictRequest)
		defer func() {
			req.Reset()
			predictReqPool.Put(req)
		}()

		// copy fields from plain struct into proto request
		req.SepalLength = body.SepalLength
		req.SepalWidth  = body.SepalWidth
		req.PetalLength = body.PetalLength
		req.PetalWidth  = body.PetalWidth

		ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
		defer cancel()

		// ✓ irisClient, not client
		resp, err := irisClient.IrisPredict(ctx, req)
		if err != nil {
			httpRequestsTotal.WithLabelValues("/predict/iris", "500").Inc()
			slog.Error("iris service call failed", "error", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		httpRequestsTotal.WithLabelValues("/predict/iris", "200").Inc()
		c.JSON(http.StatusOK, gin.H{
			"result": resp.ClassName,
			"id":     resp.ClassId,
			"source": "Go Gateway -> Python AI (Iris)",
		})

	})

	//models qwen
	r.POST("/predict/model", func(c *gin.Context) {

		timer := prometheus.NewTimer(httpDuration.WithLabelValues("/predict"))
		defer timer.ObserveDuration()

		var reqJson struct {
			Prompt string `json:"prompt" binding:"required"`
		}
		if err := c.ShouldBindJSON(&reqJson); err != nil {
			httpRequestsTotal.WithLabelValues("/predict", "400").Inc()
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		ctx, cancel := context.WithTimeout(c.Request.Context(), 60*time.Second)
		defer cancel()
		resp, err := modelClient.ModelPredict(ctx, &modelv1.ModelPredictRequest{
			Prompt: reqJson.Prompt,
		})
		if err != nil {
			httpRequestsTotal.WithLabelValues("/predict", "500").Inc()
			slog.Error("model service call failed", "error", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		httpRequestsTotal.WithLabelValues("/predict", "200").Inc()
		c.JSON(http.StatusOK, gin.H{
			"reply":  resp.Response,
			"model":  resp.ModelName,
			"source": "Go Gateway -> Ollama (Qwen)",
		})
	})

	slog.Info("Go Gateway running on http://localhost:8080")
	r.Run(":8080")
}

//go get go.opentelemetry.io/otel go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc
