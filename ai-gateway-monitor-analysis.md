# AI Gateway Monitor — 深度项目分析

> 一个生产级 AI 推理平台的完整实现，涵盖微服务、gRPC、可观测性、K8s 自动扩缩容。

---

## 一、项目一句话定位

> 用 **Go REST 网关** 承接客户端 HTTP 请求，通过 **gRPC** 转发到 **Python AI 推理服务**（scikit-learn 分类 + Ollama LLM），并配备完整的 **Prometheus 指标采集、Jaeger 分布式追踪、Grafana 可视化、K8s HPA 自动扩缩容** 链路。

**面试开场（20秒模板）**：

> "这个项目实现了一个 AI 推理网关平台：Go 层做 HTTP→gRPC 转换和统一观测，Python 层做模型推理（传统 ML + LLM）。亮点是用 p99 延迟驱动 K8s HPA 自动扩缩容，从指标上报、Prometheus 采集、Adapter 转换到 HPA 决策形成完整链路。"

---

## 二、架构总览

```
┌──────────────┐
│   Client     │  HTTP (REST + SSE streaming)
└──────┬───────┘
       │
       ▼
┌─────────────────────────────────────────┐
│  go-gateway  (Gin · :8080)              │
│  ┌─────────────────────────────────┐    │
│  │ /predict/iris     → gRPC unary │    │
│  │ /predict/model    → gRPC unary │    │
│  │ /predict/model/stream → SSE    │    │
│  │ /health /metrics               │    │
│  └─────────────────────────────────┘    │
│  • Prometheus metrics (promhttp)        │
│  • OpenTelemetry tracing (OTLP gRPC)    │
│  • pprof (:6060)                        │
└──────────┬──────────────────────────────┘
           │ gRPC (protobuf, insecure, :50051)
           ▼
┌─────────────────────────────────────────┐
│  python-ai  (gRPC server :50051)        │
│  ┌─────────────────────────────────┐    │
│  │ IrisPredictor  (RandomForest)  │    │
│  │ ModelPredictor (Ollama client) │    │
│  └─────────────────────────────────┘    │
│  • OpenTelemetry tracing (OTLP gRPC)    │
│  • Structured logging                   │
└──────────┬──────────────────────────────┘
           │ HTTP (Ollama REST API)
           ▼
┌──────────────────┐
│  Ollama Server   │
│  (Qwen2.5:1.5b) │
│  :11434          │
└──────────────────┘

Observability Stack (sidecar pattern):
  Prometheus :9090  ← 抓取 /metrics
  Grafana    :3000  ← 仪表板可视化
  Jaeger     :16686 ← 分布式追踪 UI
  GPU Exporter :9835 ← GPU 指标（独立进程）
```

---

## 三、核心数据流（三条路径）

### 路径 1：传统 ML 推理（Iris 分类）

```
Client POST /predict/iris
  → Gin 路由 → IrisHandler.Predict()
    → 解析 JSON → sync.Pool 复用 protobuf 请求对象
    → context.WithTimeout(3s)
    → gRPC IrisPredictorClient.IrisPredict()
      → Go otelgrpc 注入 trace context 到 gRPC metadata
      → Python IrisPredictor.IrisPredict()
        → RandomForest.predict()
        → 返回 class_name + class_id
    → 记录 HTTP/gRPC duration metrics
    → 返回 JSON
```

### 路径 2：LLM 推理（普通）

```
Client POST /predict/model
  → Gin 路由 → ModelHandler.Predict()
    → 校验 prompt 长度（≤2000）
    → context.WithTimeout(60s)
    → gRPC ModelPredictorClient.ModelPredict()
      → Python ModelPredictor.ModelPredict()
        → ollama.Client.generate(stream=False)
        → 返回 response + eval_count + eval_duration
    → 记录 AI token / generation duration metrics
    → 返回 JSON（含模型名、token 统计、耗时）
```

### 路径 3：LLM 流式推理（SSE）

```
Client POST /predict/model/stream
  → Gin 路由 → ModelHandler.PredictStream()
    → 设置 SSE headers (text/event-stream)
    → gRPC ModelPredictStream (server streaming)
      → Python ModelPredictor.ModelPredictStream()
        → ollama.Client.generate(stream=True)
        → yield 每个 chunk
    → Gin c.Stream() 逐块写入 SSE event
    → 末尾发送 event: done
```

**关键设计点**：
- SSE 流式输出让用户看到逐 token 生成，体验更好
- gRPC server streaming → SSE 的桥接代码很经典
- 流式场景下 token 统计在最后一个 chunk 才完整

---

## 四、可观测性体系（三层打点）

### 4.1 指标层（Prometheus Metrics）

| 指标名 | 类型 | Label | 用途 |
|--------|------|-------|------|
| `http_requests_total` | Counter | `path`, `status` | QPS 计算、错误率 |
| `http_request_duration_seconds` | Histogram | `path` | HTTP 延迟分布、p99 计算 |
| `grpc_request_duration_seconds` | Histogram | `method`, `status` | gRPC 调用延迟 |
| `ai_generated_tokens_total` | Counter | `model` | Token 产出量 |
| `ai_generation_duration_seconds` | Histogram | `model` | 模型纯推理耗时 |

**为什么用 Histogram 而不是 Summary？**
- Histogram 可以在 Prometheus 端用 `histogram_quantile()` 动态算任意分位值（p50/p90/p95/p99）
- Summary 的分位值在客户端固化，无法跨实例聚合
- Histogram bucket 设计需要权衡精度和开销——本项目 HTTP bucket 在 ms 级别细粒度，AI bucket 在秒级别

**gRPC 指标的打点策略**：
```go
// 在 Go 侧 handler 中手动计时 gRPC 调用
grpcStart := time.Now()
resp, err := h.client.ModelPredict(ctx, req)
metrics.GRPCRequestDuration.WithLabelValues("ModelPredict", status).
    Observe(time.Since(grpcStart).Seconds())
```
- 不依赖 server 端暴露的指标，在 client 端主动打点
- 可以区分不同 method + status，便于定位是哪个下游方法慢

### 4.2 追踪层（OpenTelemetry + Jaeger）

**Go 侧**：
```go
// telemetry/tracer.go
exporter, _ := otlptracegrpc.New(ctx, otlptracegrpc.WithEndpoint(jaegerEndpoint))
tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exporter))
otel.SetTextMapPropagator(propagation.TraceContext{})
```
- `BatchSpanProcessor`：批量异步上报，降低对请求延迟的影响
- `TraceContext` 传播器：兼容 W3C Trace Context 标准

**gRPC 自动埋点**：
```go
// grpcclient/client.go — Go client 侧
grpc.WithStatsHandler(otelgrpc.NewClientHandler())
```
```python
# observability.py — Python server 侧
GrpcInstrumentorServer().instrument()
```
- 无需手写 span 代码，gRPC 调用自动产生 client/server span
- trace context 通过 gRPC metadata 自动传播

**服务名标识**：
- Go: `semconv.ServiceNameKey.String("go-gateway")`
- Python: `Resource.create({"service.name": "python-ai"})`
- 在 Jaeger UI 中可按服务名过滤和查看调用链

### 4.3 日志层

**Python 结构化日志**：
```python
logging.basicConfig(
    format="%(asctime)s [%(levelname)s] %(name)s: %(message)s",
    datefmt="%Y-%m-%dT%H:%M:%S",
)
```
- 统一日志格式方便 grep/ELK 解析
- 每个请求在日志中有明确的上下文（prompt 截断、tokens 统计）

**Go 侧使用 `log/slog`**：
- Go 1.21+ 的结构化日志标准库
- 支持 key=value 形式的上下文信息

---

## 五、部署模式对比

### 5.1 Docker Compose（本地开发）

```
docker compose up --build
```

**关键配置分析**：

```yaml
# 容器到宿主机通信
extra_hosts:
  - "host.docker.internal:host-gateway"
```
- `host.docker.internal` 是 Docker 的特殊 DNS 名，自动解析为宿主机 IP
- `host-gateway` 不是固定 IP 字符串，而是让 Docker 动态解析
- 这样 Python 容器通过 `http://host.docker.internal:11434` 访问宿主机的 Ollama
- **跨平台兼容**：Linux/WSL/macOS/Windows 都能用同一个配置

```yaml
# 容器间通信
AI_SERVICE_ADDR=python-ai:50051
```
- 同一 `ai-network` 桥接网络内，容器名就是 DNS 名
- 无需硬编码 IP

```yaml
# 健康检查与启动顺序
depends_on:
  python-ai:
    condition: service_healthy
```
- `condition: service_healthy` 是 Compose v3.9+ 的特性
- Go 网关要等到 Python gRPC 端口就绪后才启动
- Python 的 healthcheck 用 socket 连接检测，比简单 `curl` 更轻量

**Grafana Dashboard**：预置 12 块面板分为 4 行：
1. AI Inference：QPS、Token 速率、推理延迟 p50/p95/p99
2. Gateway：HTTP 延迟、gRPC 延迟、5xx 错误率
3. GPU Resources：利用率、显存、温度
4. Go Runtime：Goroutine 数、GC 耗时、RSS 内存

---

### 5.2 Kubernetes（生产部署）

**部署架构**：

```
┌─────────────────────────────────────────────┐
│  Kubernetes Cluster                         │
│                                             │
│  ┌──────────────────┐  ┌────────────────┐  │
│  │ go-gateway       │  │ python-ai      │  │
│  │ Deployment: 2    │  │ Deployment: 1  │  │
│  │ Service:         │  │ Service:       │  │
│  │  web:80→8080    │  │  :50051        │  │
│  │  pprof:6060      │  │                │  │
│  └────────┬─────────┘  └───────┬────────┘  │
│           │                    │           │
│           └──── gRPC ─────────┘           │
│                                             │
│  ┌──────────────────┐                       │
│  │ ollama-svc       │  ExternalName        │
│  │ Service+Endpoints│  → 172.17.141.222   │
│  └──────────────────┘                       │
│                                             │
│  Monitoring (run in Docker Compose):        │
│    Prometheus + Grafana + Jaeger           │
└─────────────────────────────────────────────┘
```

**关键设计决策**：

1. **Ollama 用 Service + Endpoints 而非 ExternalName**：
```yaml
# ollama-svc.yaml
apiVersion: v1
kind: Service
metadata:
  name: ollama-svc
spec:
  ports:
    - port: 11434
---
apiVersion: v1
kind: Endpoints
metadata:
  name: ollama-svc
subsets:
  - addresses:
      - ip: 172.17.141.222  # WSL 宿主机的 eth0 IP
    ports:
      - port: 11434
```
- ExternalName Service 在某些 CNI 中有 DNS 兼容问题
- Endpoints 手动指定外部 IP 更稳定，适合将集群外服务映射到集群内

2. **Go Deployment replicas: 2**：
- 网关是无状态的，水平扩展提升吞吐
- `python-ai replicas: 1`：模型推理是 GPU 密集型，单副本避免资源争抢

3. **监控栈留 Docker Compose**：
- Prometheus/Grafana/Jaeger 不需要每次部署
- `deploy.sh` 用 `patch_*` 函数动态替换 IP，适配 WSL IP 漂移

---

## 六、自动扩缩容链路（完整深度剖析）

这是本项目的**核心技术亮点**。

### 6.1 完整数据流

```
go-gateway Pod
  │
  │ http_request_duration_seconds_bucket{path, le}
  │ GET /metrics (promhttp)
  ▼
Prometheus（由 ServiceMonitor 发现目标）
  │
  │ 储存在 TSDB 中
  ▼
Prometheus Adapter（adapter-values.yaml）
  │
  │ histogram_quantile(0.99, sum by (le, namespace, pod)
  │   (rate(http_request_duration_seconds_bucket[1m])))
  │ → 重命名为 p99_latency
  ▼
K8s Custom Metrics API (/apis/custom.metrics.k8s.io/v1beta1)
  │
  │ kubectl get --raw "/apis/custom.metrics.k8s.io/v1beta1/.../p99_latency"
  ▼
HPA Controller（go-gateway-hpa.yaml）
  │
  │ desiredReplicas = ceil[currentReplicas * (currentMetric / targetMetric)]
  ▼
Deployment/go-gateway 调整 replicas
```

### 6.2 逐层配置解析

**第一层：应用打点**
```go
HTTPDuration = prometheus.NewHistogramVec(
    prometheus.HistogramOpts{
        Name: "http_request_duration_seconds",
        Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
    },
    []string{"path"},
)
```
- 自定义 bucket 分布：毫秒级密集（匹配 Web 请求延迟），到秒级稀疏
- `path` label 允许按路由维度分析

**第二层：ServiceMonitor 发现**
```yaml
# go-gateway-monitor.yaml
metadata:
  labels:
    release: prometheus-kube-prometheus  # ← 必须匹配 Prometheus CR 的 selector
spec:
  selector:
    matchLabels:
      app: go-gateway  # 选中对应的 Service
  endpoints:
    - port: web        # 对应 Service 中 port name
      path: /metrics
```
- **关键断点**：`release` label 不匹配 → Prometheus Operator 忽略此 ServiceMonitor
- Service 必须有 `app: go-gateway` label
- `port: web` 必须与 Service yaml 中 `ports[].name: web` 一致

**第三层：Adapter 指标转换**
```yaml
# adapter-values.yaml
rules:
  custom:
    - seriesQuery: '{__name__="http_request_duration_seconds_bucket"}'
      resources:
        overrides:
          kubernetes_namespace: { resource: "namespace" }
          kubernetes_pod_name: { resource: "pod" }
      name:
        matches: "^http_request_duration_seconds_bucket"
        as: "p99_latency"
      metricsQuery: >
        histogram_quantile(0.99,
          sum by (le, kubernetes_namespace, kubernetes_pod_name)
            (rate(http_request_duration_seconds_bucket[1m])))
```
- **`seriesQuery`**：从所有时间序列中筛选出 bucket 指标
- **`resources.overrides`**：把 Prometheus label 映射到 K8s 资源概念
- **`name.as`**：重命名为 HPA 可用的指标名
- **`metricsQuery`**：真正的 PromQL 计算逻辑
  - `rate(...[1m])`：1 分钟窗口的每秒增长速率
  - `sum by (le, namespace, pod)`：按 bucket 边界、命名空间、Pod 聚合
  - `histogram_quantile(0.99, ...)`：从 bucket 分布反推 p99 值

**第四层：HPA 决策**
```yaml
# go-gateway-hpa.yaml
metrics:
  - type: Pods
    pods:
      metric:
        name: p99_latency
      target:
        type: AverageValue
        averageValue: "500m"  # 500ms
```
- `type: Pods`：使用 Pod 维度的指标
- `averageValue: "500m"`：K8s 数量系统中 `500m` = 0.5 = 500ms
  - 注意：这里单位是**秒**，不是毫秒
  - `500m` = 500 milli-seconds = 0.5 秒 = 500ms ✓
- 扩容算法：`desired = ceil[current * (currentP99 / 500ms)]`

### 6.3 常见断点与排查

| 断点 | 现象 | 排查方式 |
|------|------|----------|
| ServiceMonitor 不生效 | Prometheus Targets 里看不到 go-gateway | `kubectl get servicemonitor -o yaml` 检查 label |
| Adapter 找不到指标 | `kubectl get --raw /apis/custom.metrics.k8s.io/...` 返回空 | 检查 `seriesQuery` 是否匹配，Prometheus 里是否有 `_bucket` 后缀 |
| HPA 显示 `unknown` | `kubectl get hpa` 显示 unknown/0% | `kubectl describe hpa` 看 events，通常是 Adapter 未暴露指标 |
| 扩缩容抖动 | replicas 频繁变化 | 增大 `rate()` 窗口 `[1m]→[5m]`，或加 `--horizontal-pod-autoscaler-tolerance` |

---

## 七、代码设计亮点

### 7.1 Go 侧

**`sync.Pool` 复用 protobuf 对象**：
```go
type IrisHandler struct {
    reqPool sync.Pool
}
func NewIrisHandler(...) *IrisHandler {
    return &IrisHandler{
        reqPool: sync.Pool{
            New: func() interface{} { return new(irisv1.IrisPredictRequest) },
        },
    }
}
// 使用：
req := h.reqPool.Get().(*irisv1.IrisPredictRequest)
defer func() { req.Reset(); h.reqPool.Put(req) }()
```
- 高 QPS 下减少 GC 压力
- `Reset()` 是 protobuf 生成的方法，清空字段复用内存

**优雅关闭**：
```go
quit := make(chan os.Signal, 1)
signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
<-quit
shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.GRPCKeepAliveTimeout)
srv.Shutdown(shutdownCtx)
```
- 收到信号后不立即杀进程，等待现有请求处理完
- `context.WithTimeout` 防止无限等待

**pprof 独立端口**：
```go
go func() {
    http.ListenAndServe(cfg.PProfAddr, nil)  // :6060
}()
```
- pprof 不暴露在 8080 业务端口，安全隔离
- 生产环境可只对内网开放

**gRPC 连接配置**：
```go
grpc.WithKeepaliveParams(keepalive.ClientParameters{
    Time:    10 * time.Second,   // 每 10s 发 ping
    Timeout: 3 * time.Second,    // 3s 等不到 pong 判定断开
    PermitWithoutStream: true,   // 无活跃流也发 keepalive
})
```
- `PermitWithoutStream: true` 很重要：防止空闲连接因无流量被中间代理关闭

### 7.2 Python 侧

**依赖管理用 uv**：
- `uv sync --frozen` 精确复现依赖版本
- 比 pip + requirements.txt 更现代、更快
- `uv.lock` 锁定所有传递依赖

**模型可选加载路径**：
```python
def _load_model(self, model_path: Optional[str]):
    if model_path and os.path.exists(model_path):
        return pickle.load(f)   # 加载预训练模型
    # 否则内存训练
    clf = RandomForestClassifier()
    clf.fit(self._iris_meta.data, self._iris_meta.target)
```
- 生产环境用预训练模型确保一致性
- 开发/演示时内存训练，无需模型文件

**gRPC 错误码映射**：
```python
except ollama.ResponseError as e:
    context.set_code(StatusCode.UNAVAILABLE)
    context.set_details(f"Model backend error: {e}")
```
- 区分业务错误（UNAVAILABLE）和内部错误（INTERNAL）
- Go 网关根据 gRPC status code 做不同的处理

**流式响应中的生成器**：
```python
def ModelPredictStream(self, request, context):
    stream = self._client.generate(stream=True)
    for chunk in stream:
        yield model_pb2.ModelPredictResponse(...)
```
- Python `yield` 天然适合 gRPC server streaming
- 每个 chunk 即时发送，不缓存整个响应

### 7.3 Proto 代码生成（Buf）

```yaml
# buf.gen.yaml — 一次生成 Go + Python 双端代码
plugins:
  - remote: buf.build/protocolbuffers/go → service_go/gen
  - remote: buf.build/grpc/go           → service_go/gen
  - remote: buf.build/protocolbuffers/python → service_python/gen
  - remote: buf.build/grpc/python           → service_python/gen
```

**为什么用 Buf 而不是 protoc？**
- 无需本地安装 protoc 和各种插件
- 远程插件保证版本一致性
- `buf lint` 和 `buf breaking` 可以做 API 治理
- 多语言代码生成只需一个 `buf generate` 命令

---

## 八、GPU Exporter（独立组件）

```python
# 死循环每 5 秒调 nvidia-smi
while True:
    collect()
    time.sleep(5)
```

**设计特点**：
- **独立进程**：不嵌入 Python AI 服务，降低耦合
- **Gauge 类型**：GPU 利用率/显存/温度是瞬时值，会上上下下
- **label `gpu`**：支持多卡场景
- **不依赖 Docker**：直接在宿主机运行，因为 nvidia-smi 需要访问 GPU 驱动

**Prometheus 抓取**：
```yaml
- job_name: "gpu"
  static_configs:
    - targets: ["172.17.141.222:9835"]  # 宿主机 IP
```

---

## 九、负载测试（k6）

```
k6 负载模型：
  Ramp-up (15s, 0→10 VU)
  → Steady (30s, 10 VU)
  → Spike  (15s, 10→30 VU)
  → Hold   (30s, 30 VU)
  → Ramp-down (10s, 30→0 VU)
```

**阈值设计**：
```javascript
thresholds: {
  "iris_duration_ms":  ["p(95)<500"],      // Iris 分类 p95 < 500ms
  "model_duration_ms": ["p(95)<30000"],    // LLM 推理 p95 < 30s
  "http_req_failed":   ["rate<0.01"],      // 错误率 < 1%
}
```
- Iris 是毫秒级实时分类，500ms 够严格
- LLM 推理受模型大小和 GPU 影响，30s 是合理上限
- 错误率 1% 是常见的 SLO 基线

**实测结果**（README）：峰值 QPS ~15 req/s，GPU 利用率 80%，Go RSS 仅 ~36 MiB

---

## 十、项目文件组织（面试可讲）

| 层级 | 文件 | 职责 |
|------|------|------|
| **Proto 定义** | `proto/iris/v1/iris.proto`, `proto/model/v1/model.proto` | API 契约，语言无关 |
| **代码生成** | `buf.yaml`, `buf.gen.yaml` | 一键生成 Go+Python stub |
| **Go 网关** | `service_go/main.go` | 入口：初始化→路由→启动 |
| | `service_go/config/config.go` | 环境变量读取，超时配置 |
| | `service_go/metrics/metrics.go` | 5 个 Prometheus 指标定义 |
| | `service_go/telemetry/tracer.go` | OTLP exporter 初始化 |
| | `service_go/grpcclient/client.go` | gRPC 连接池（keepalive） |
| | `service_go/handlers/` | 3 个 handler（health/iris/model） |
| | `service_go/router/router.go` | Gin 路由注册 |
| **Python AI** | `service_python/main.py` | 组装：模型→server |
| | `service_python/server.py` | gRPC server 创建+优雅关闭 |
| | `service_python/observability.py` | 日志+tracing 初始化 |
| | `service_python/models/iris_predictor.py` | scikit-learn RandomForest |
| | `service_python/models/ollama_predictor.py` | Ollama API 封装 |
| | `service_python/gpu_exporter.py` | 独立 GPU 指标导出 |
| **部署** | `docker-compose.yml` | 本地一键启动 5 个服务 |
| | `go-gateway.yaml` | K8s Deployment + Service |
| | `python-ai.yaml` | K8s Deployment + Service |
| | `ollama-svc.yaml` | 外部 Ollama 的 K8s Service |
| **监控** | `prometheus.yml` | 静态抓取目标（本地） |
| | `go-gateway-monitor.yaml` | ServiceMonitor（K8s） |
| | `adapter-values.yaml` | Prometheus Adapter 规则 |
| | `go-gateway-hpa.yaml` | HPA 自动扩缩容 |
| **测试** | `test/test.js` | k6 混合负载测试 |

---

## 十一、面试高频追问 & 回答指南

### Q1: 为什么 Go 做网关，Python 做推理？

**答**：
- Go：高并发 HTTP 处理（Gin + goroutine）、低内存（实测 RSS 36MB）、编译为单一二进制部署简单
- Python：ML 生态丰富（scikit-learn、Ollama client）、模型推理是主要瓶颈而非网关
- 语言边界用 gRPC + protobuf 隔离，各自独立部署和扩容

### Q2: gRPC 为什么用 insecure？

**答**：
- 内网通信 + demo 项目简化配置
- 生产环境应该上 mTLS 或至少 TLS
- 代码结构已预留升级空间（`grpc.WithTransportCredentials` 替换 `insecure.NewCredentials()` 即可）

### Q3: 为什么 HPA 用 p99 而不是平均延迟？

**答**：
- 平均值会被大量快请求拉低，掩盖尾部慢请求
- p99 反映最慢的 1% 用户体验
- 当 p99 恶化时扩容，确保绝大多数用户不受尾部延迟影响
- 这是生产级 SLO 驱动的扩容策略

### Q4: Prometheus Adapter 中的 `500m` 到底是多少？

**答**：
- `500m` 在 K8s 数量系统中 = 0.5
- 由于 `p99_latency` 单位是秒，`0.5` = 500ms
- 如果实际 p99 是 0.8 秒（800ms），则 `current/target = 0.8/0.5 = 1.6`，触发扩容

### Q5: Go 多阶段构建，Python 为什么不用？

**答**：
- Go 编译后是单一静态二进制，运行阶段只需 `alpine` + 二进制，镜像可以从 800MB→15MB
- Python 运行时仍需解释器 + 所有依赖，瘦身空间有限
- Python Dockerfile 用了 `python:3.12-slim` 已经是较小的基础镜像
- 高级做法：用 multi-stage 把 `uv sync` 的 .venv 复制到 runtime stage

### Q6: 如果 Ollama 不可用怎么处理？

**答**：
- Python 侧 catch `ollama.ResponseError` → gRPC `UNAVAILABLE` → Go 侧返回 HTTP 500
- Go 侧有 `context.WithTimeout` 防止无限等待
- 健康检查会检测端口连通性
- 生产环境应该加熔断器（circuit breaker）和重试策略

### Q7: 为什么监控栈留 Docker Compose 而不放 K8s？

**答**：
- 监控栈是辅助设施，独立于业务服务生命周期
- 方便本地开发和 K8s 部署共用同一套监控
- K8s 生产环境通常会换成 Prometheus Operator + Grafana Operator

---

## 十二、可以提的改进方向（展示思考深度）

1. **安全加固**
   - gRPC 上 mTLS
   - HTTP 加认证中间件（JWT/API Key）
   - pprof 端口限制内网访问

2. **高可用增强**
   - Python AI 多副本 + gRPC load balancing（headless service + client-side LB）
   - Go 网关加 rate limiting
   - Ollama 不可用时 circuit breaker + fallback

3. **可观测性深化**
   - 加 Exemplar 把 trace ID 关联到 metric
   - 结构化日志输出 JSON → Loki 采集
   - 告警规则（AlertManager）：p99 > 1s 持续 5min 触发告警

4. **CI/CD**
   - GitHub Actions：lint → test → build image → push to registry
   - ArgoCD 或 Flux 做 GitOps 部署

5. **模型服务化**
   - 模型热加载（不重启更新模型）
   - A/B 测试框架（按流量比例路由到不同模型版本）
   - 模型性能回归测试

---

## 十三、速查表

### 端口速查

| 服务 | 端口 | 协议 |
|------|------|------|
| Go Gateway API | 8080 | HTTP |
| Go pprof | 6060 | HTTP |
| Python AI gRPC | 50051 | gRPC |
| Ollama | 11434 | HTTP |
| Prometheus | 9090 | HTTP |
| Grafana | 3000 | HTTP |
| Jaeger UI | 16686 | HTTP |
| Jaeger OTLP | 4317 | gRPC |
| GPU Exporter | 9835 | HTTP |

### 关键命令速查

```bash
# 本地开发
docker compose up --build

# K8s 部署
./deploy.sh              # 一键部署
./deploy.sh forward      # 端口转发（让 Prometheus 能抓取）
./deploy.sh gpu          # 启动 GPU exporter

# 测试
curl -X POST localhost:8080/predict/iris -H 'Content-Type: application/json' \
  -d '{"sepal_length":6.0,"sepal_width":3.0,"petal_length":5.5,"petal_width":2.0}'

curl -X POST localhost:8080/predict/model -H 'Content-Type: application/json' \
  -d '{"prompt":"Explain ML in one sentence."}'

# 负载测试
docker run --rm -i --network host grafana/k6 run - < test/test.js

# 验证 HPA 链路
kubectl get hpa
kubectl get --raw "/apis/custom.metrics.k8s.io/v1beta1/namespaces/default/pods/*/p99_latency" | jq
```

---

> **最后建议**：面试时不要试图背下所有细节。记住三层链路（**请求链路 / 监控链路 / 扩容链路**），每层能用一两句话说清楚，然后根据面试官的追问展开具体细节即可。

---

## 附：Kubernetes 零基础入门（结合本项目）

### 一、先理解"为什么要 K8s"

Docker Compose 管一台机器上的容器，K8s 管**多台机器组成的集群**。

```
Docker Compose：                Kubernetes：
一台服务器                      多台服务器组成的集群
  ├─ 容器A                        ├─ 节点1：Pod-A1  Pod-B1
  ├─ 容器B                        ├─ 节点2：Pod-A2  Pod-C1
  └─ 容器C                        └─ 节点3：Pod-B2  Pod-C2

你手动启停                     K8s 自动调度、重启、扩缩
```

本项目用 K8s 解决了三个问题：
1. **服务发现**：Go 网关自动找到 Python AI（不用硬编码 IP）
2. **自愈**：Pod 挂了自动重启
3. **自动扩缩**：流量高了自动加副本

---

### 二、最核心的 6 个概念（按理解顺序）

#### 概念 1：Pod（最小调度单位）

Pod 是 K8s 里**最小的运行单元**，一个 Pod 里面运行一个或多个容器。

```
┌────────── Pod ──────────┐
│  go-gateway 容器         │  ← 你的 Go 应用
│  (可选：sidecar 容器)     │  ← 比如日志收集
└─────────────────────────┘
```

- 大部分情况下 **1 Pod = 1 容器**（本项目就是）
- Pod 是**临时的**：挂了就新建，IP 会变，名字也会变
- **你不能直接创建 Pod**，要通过 Deployment 来管

#### 概念 2：Deployment（管 Pod 的控制器）

你告诉 K8s "我要 2 个 go-gateway Pod"，Deployment 保证**始终有 2 个在跑**。

```yaml
# go-gateway.yaml（节选）
apiVersion: apps/v1
kind: Deployment
metadata:
  name: go-gateway              # Deployment 的名字
spec:
  replicas: 2                   # ← "我要 2 个 Pod"
  selector:
    matchLabels:
      app: go-gateway           # 如何识别"属于我的 Pod"
  template:                     # Pod 模板（定义 Pod 长什么样）
    metadata:
      labels:
        app: go-gateway         # Pod 的标签（必须匹配上面的 selector）
    spec:
      containers:
        - name: go-gateway
          image: go-gateway:v2  # 用什么镜像
          ports:
            - containerPort: 8080
```

**类比**：Deployment 是"车间主任"，Pod 是"工人"。主任保证班上始终有 N 个工人。

**关键字段**：
| 字段 | 含义 |
|------|------|
| `replicas` | 要几个 Pod |
| `selector.matchLabels` | 怎么找到"我的 Pod" |
| `template.metadata.labels` | Pod 出生就贴的标签 |
| `template.spec.containers` | Pod 里跑什么容器 |

#### 概念 3：Service（稳定的访问入口）

Pod IP 会变（重启就换），你不能用 IP 访问。Service 提供一个**固定的虚拟 IP + DNS 名**。

```
不稳定的 Pod IP：                 Service 提供的稳定入口：
                                  ┌────────────────────┐
Pod-A  (IP: 10.0.0.1) ──┐         │                     │
Pod-A' (IP: 10.0.0.5) ──┼────────→│  go-gateway-svc     │
Pod-A''(IP: 10.0.0.9) ──┘         │  10.96.0.10:80     │
                                  │  DNS: go-gateway-svc│
                                  └────────────────────┘
```

```yaml
# go-gateway.yaml（节选）
apiVersion: v1
kind: Service
metadata:
  name: go-gateway-svc           # 集群内 DNS 名
  labels:
    app: go-gateway              # Service 自己的标签（供 ServiceMonitor 用）
spec:
  selector:
    app: go-gateway              # 把流量转发到带有此标签的 Pod
  ports:
    - name: web                  # ← 端口名字（ServiceMonitor 要用）
      protocol: TCP
      port: 80                   # Service 对外暴露的端口
      targetPort: 8080           # Pod 里容器监听的端口
```

**流量路径**：`客户端 → go-gateway-svc:80 → 随机选一个 Pod → Pod 的 8080 端口`

**关键字段**：
| 字段 | 含义 |
|------|------|
| `selector.app: go-gateway` | 流量转发到标签 `app=go-gateway` 的 Pod |
| `port` | Service 自己的端口 |
| `targetPort` | Pod 里容器的端口 |
| `name: web` | 端口名称，ServiceMonitor 用它来引用 |

#### 概念 4：Label 和 Selector（粘合剂）

Label 是贴在 K8s 对象上的**标签**（key=value），Selector 用来**按标签筛选**。

```
Deployment 创建 Pod 时：
  Pod 标签:  app: go-gateway, version: v2
               ↑                      ↑
               │                      │
Service selector:  app: go-gateway ──┘（匹配！）
ServiceMonitor selector:  release: prometheus ──┘（不匹配！ServiceMonitor 不管这个）
```

本项目里的标签流转：

```
Deployment.template.labels       →   Pod.labels
  app: go-gateway                     app: go-gateway
                                      ↓
Service.selector                     Service.metadata.labels
  app: go-gateway          ← 匹配 →    app: go-gateway
                                      ↓
                               ServiceMonitor.selector
                                 app: go-gateway          ← 匹配！
                               ServiceMonitor.labels
                                 release: prometheus-...  ← 匹配 Prometheus CR！
```

#### 概念 5：ExternalName / Endpoints（访问集群外服务）

集群内的服务（go-gateway、python-ai）可以通过 Service 互相访问。那**集群外的 Ollama** 怎么办？

本项目用了 `Service + Endpoints` 的方式：

```yaml
# ollama-svc.yaml
---
apiVersion: v1
kind: Service
metadata:
  name: ollama-svc                # 集群内 DNS 名
spec:
  ports:
    - port: 11434
      targetPort: 11434
# 注意：这里没有 selector！

---
apiVersion: v1
kind: Endpoints
metadata:
  name: ollama-svc                # 必须跟 Service 同名
subsets:
  - addresses:
      - ip: 172.17.141.222        # 硬编码的外部 Ollama IP
    ports:
      - port: 11434
```

**原理**：
- Service 没写 `selector` → K8s 不会自动创建 Endpoints
- 你手动创建了同名 Endpoints → K8s 把两者绑定
- Pod 访问 `ollama-svc:11434` → DNS 解析到 Service IP → 转发到 `172.17.141.222:11434`

```
集群内 Pod (python-ai)
    │
    │ http://ollama-svc:11434
    ▼
┌─────────────┐     ┌──────────────┐     ┌──────────────────┐
│  Service    │────→│  Endpoints   │────→│  外部 Ollama      │
│ ollama-svc  │     │ 172.17...222 │     │ 172.17.141.222   │
└─────────────┘     └──────────────┘     └──────────────────┘
```

**对比 ExternalName Service**：

```yaml
# 另一种写法（本项目没用）：
kind: Service
spec:
  type: ExternalName
  externalName: my-ollama.example.com  # DNS CNAME
```

ExternalName 本质是 DNS CNAME，某些 CNI 不支持或有兼容问题。手动 Endpoints 更稳定。

#### 概念 6：HPA（自动扩缩容）

Horizontal Pod Autoscaler，根据指标自动增减 Deployment 的 replicas。

```yaml
# go-gateway-hpa.yaml
spec:
  scaleTargetRef:
    kind: Deployment
    name: go-gateway        # ← 控制哪个 Deployment
  minReplicas: 2            # 最少 2 个
  maxReplicas: 10           # 最多 10 个
  metrics:
    - type: Pods
      pods:
        metric:
          name: p99_latency # ← 看的指标
        target:
          type: AverageValue
          averageValue: "500m"  # ← 阈值：0.5 秒
```

**扩缩容算法**（简化版）：

```
desired = current * (currentMetric / targetMetric)

例：当前 2 个副本，p99 = 0.8s，阈值 = 0.5s
  desired = 2 * (0.8 / 0.5) = 2 * 1.6 = 3.2 → 向上取整 = 4
```

**HPA 怎么拿到 p99_latency？** 这条链路是项目核心，单独展开：

```
Go 应用代码                  暴露 /metrics
    │  http_request_duration_seconds_bucket
    ▼
Prometheus                  由 ServiceMonitor 告诉 Prometheus 去抓
    │  TSDB 存储
    ▼
Prometheus Adapter          把 PromQL 结果转成 K8s Custom Metrics API
    │  histogram_quantile(0.99, sum by (le, pod)(rate(...[1m])))
    │  重命名为 p99_latency
    ▼
K8s Custom Metrics API      /apis/custom.metrics.k8s.io/v1beta1/...
    │  HPA Controller 每 15s 查一次
    ▼
HPA                         对比 p99_latency 和 500m，决定扩/缩
    │
    ▼
Deployment/replicas         调整副本数
```

**省流版**：代码打点 → Prometheus 存 → Adapter 算 p99 → HPA 读 → 改副本数

---

### 三、Health Check 在 K8s 中的三种 Probe

对比 Docker Compose 只有一个 `healthcheck`，K8s 有三种探针：

| 探针 | 作用 | 本项目用法 |
|------|------|-----------|
| `livenessProbe` | Pod 还活着吗？**死了就重启** | Go：HTTP GET `/metrics` |
| `readinessProbe` | Pod 能接流量吗？**不能就摘除** | Python：TCP Socket 50051 |
| `startupProbe` | Pod 启动完了吗？（慢启动保护） | 本项目未用（用了 `initialDelaySeconds`） |

```yaml
# go-gateway.yaml — 存活性探针
livenessProbe:
  httpGet:
    path: /metrics            # 访问 /metrics 判断存活
    port: 8080
  initialDelaySeconds: 10    # 启动后等 10 秒才开始检查
  periodSeconds: 10          # 每 10 秒检查一次
```

```yaml
# python-ai.yaml — 就绪性探针
readinessProbe:
  tcpSocket:
    port: 50051              # 端口能连上 = 就绪
  initialDelaySeconds: 5
  periodSeconds: 10
```

**区别理解**：

```
Pod 启动 → startupProbe（通过）→ 开始 liveness + readiness

livenessProbe 失败：  "容器疯了，杀掉重启"
readinessProbe 失败： "容器活着但暂时不能干活，从 Service 摘掉"

例：OOM 导致死循环 → liveness 失败 → 重启
例：依赖的 Ollama 挂了 → readiness 失败 → 不接流量，等恢复
```

---

### 四、deploy.sh 看懂每一步

这个脚本做了一次完整部署。按执行顺序拆解：

```bash
./deploy.sh          # 等价于 ./deploy.sh all
```

```
Step 1: check_deps
  → 确认 docker / kubectl 可用，kubectl 能连上集群

Step 2: patch_ollama_svc
  → 检测 WSL 的 eth0 IP
  → 替换 ollama-svc.yaml 里的 172.17.141.222 为当前真实 IP

Step 3: patch_prometheus_target
  → 替换 prometheus.yml 里的抓取目标

Step 4: patch_jaeger_endpoint
  → 替换 go-gateway.yaml / python-ai.yaml 里的 Jaeger 地址

Step 5: build_images
  → docker build go-gateway:v2 和 python-ai:v2

Step 6: load_images
  → 把镜像导入 K8s 集群（minikube/k3s/kind 不同方式）

Step 7: apply_manifests
  → kubectl apply -f 每个 yaml
  → 顺序：ollama-svc → python-ai → go-gateway → hpa → servicemonitor

Step 8: rollout_restart
  → 强制重建 Pod，确保用上新镜像

Step 9: wait_for_pods
  → 等所有 Pod 变成 Running

Step 10: start_monitoring
  → docker compose up -d prometheus grafana jaeger
  → 监控栈用 Compose 跑，不走 K8s

Step 11: show_status
  → 打印 Pod/Service/HPA 状态
```

**关键理解**：
- Step 2/3/4 的 patch 是因为**WSL 每次重启 IP 会变**，需要动态替换
- Step 6 的 load 是因为本地构建的镜像不在 Docker Hub，需要手动塞进集群
- Step 10 的监控栈用 Compose 而非 K8s，是简化设计：监控不属于业务服务

---

### 五、ServiceMonitor 详解（Prometheus Operator）

**它不是原生 K8s 资源**——必须先装 Prometheus Operator 才有这个 CRD。

```yaml
# go-gateway-monitor.yaml
apiVersion: monitoring.coreos.com/v1   # ← Operator 提供的 API
kind: ServiceMonitor
metadata:
  name: go-gateway-monitor
  labels:
    release: prometheus-kube-prometheus  # ← 关键！
spec:
  selector:
    matchLabels:
      app: go-gateway     # 找哪个 Service
  endpoints:
    - port: web           # 找 Service 的哪个端口名
      path: /metrics      # 请求路径
      interval: 15s       # 抓取间隔
```

**工作原理**：

```
1. Prometheus Operator 看所有 ServiceMonitor
2. 筛选 label release=prometheus-kube-prometheus 的（匹配自己）
3. 读 selector → 找到 app: go-gateway 的 Service
4. 读 endpoints.port → 找到 Service 里 name: web 的端口
5. 组合出抓取目标：http://{Pod IP}:{targetPort}/metrics
6. 自动更新 Prometheus 配置，开始抓取
```

**最常见的坑**：`release` label 不匹配

```bash
# 你的 Prometheus CR 要求：
serviceMonitorSelector:
  matchLabels:
    release: prometheus-kube-prometheus

# 你的 ServiceMonitor labels 必须包含：
labels:
  release: prometheus-kube-prometheus   # ← 少一个字都不行
```

---

### 六、Adapter 规则详解

```yaml
# adapter-values.yaml
rules:
  custom:
    - seriesQuery: '{__name__="http_request_duration_seconds_bucket"}'
      resources:
        overrides:
          kubernetes_namespace: { resource: "namespace" }
          kubernetes_pod_name: { resource: "pod" }
      name:
        matches: "^http_request_duration_seconds_bucket"
        as: "p99_latency"
      metricsQuery: >
        histogram_quantile(0.99,
          sum by (le, kubernetes_namespace, kubernetes_pod_name)
            (rate(http_request_duration_seconds_bucket[1m])))
```

**逐行翻译**：

```
① seriesQuery
  "去 Prometheus 里找出所有名字包含 http_request_duration_seconds_bucket 的指标"

② resources.overrides
  "Prometheus 的 label kubernetes_namespace → 对应 K8s 概念 namespace"
  "Prometheus 的 label kubernetes_pod_name     → 对应 K8s 概念 pod"
  （这样 HPA 才能按 namespace/pod 维度查询）

③ name
  "把指标重命名为 p99_latency"
  （HPA 用这个名字来查）

④ metricsQuery
  "真正算的时候用这段 PromQL：
   rate(...[1m])                        # 1 分钟内的每秒速率
   sum by (le, namespace, pod)          # 按 bucket 边界 + namespace + pod 聚合
   histogram_quantile(0.99, ...)        # 算出 p99"
```

**验证命令**：

```bash
# 检查 Adapter 是否成功暴露了指标
kubectl get --raw "/apis/custom.metrics.k8s.io/v1beta1" | jq

# 看具体指标值
kubectl get --raw \
  "/apis/custom.metrics.k8s.io/v1beta1/namespaces/default/pods/*/p99_latency" | jq
```

---

### 七、一张图总结所有 K8s 资源的作用

```
                    ┌───────────────┐
                    │   Deployment  │  "我要 2 个 go-gateway Pod"
                    │  go-gateway   │
                    └───────┬───────┘
                            │ 创建和管理
                            ▼
                    ┌───────────────────┐
                    │       Pod         │  实际运行的应用实例
                    │  go-gateway-xxx   │  (IP 会变，不能直接依赖)
                    │  go-gateway-yyy   │
                    └────────┬──────────┘
                             │ label: app=go-gateway
                             ▼
                    ┌───────────────────┐
                    │     Service       │  稳定的访问入口
                    │  go-gateway-svc   │  ClusterIP + DNS
                    │  port:80→8080     │
                    └────────┬──────────┘
                             │ label: app=go-gateway
                             ▼
                    ┌───────────────────┐
                    │  ServiceMonitor   │  告诉 Prometheus 去抓谁
                    │ go-gateway-monitor│  (Prometheus Operator CRD)
                    └────────┬──────────┘
                             │
              ┌──────────────┼──────────────┐
              ▼              ▼              ▼
      ┌──────────┐   ┌──────────┐   ┌──────────┐
      │Prometheus│   │ Adapter  │   │   HPA    │
      │ 抓指标   │──▶│ 算 p99   │──▶│ 改副本数  │
      └──────────┘   └──────────┘   └────┬─────┘
                                         │
                                         ▼
                                 ┌───────────────┐
                                 │   Deployment  │  replicas: 2→4
                                 └───────────────┘
```

---

### 八、初学者自查清单

学完这个项目，你应该能回答：

- [ ] Deployment、Service、Pod 三者关系是什么？
- [ ] `selector` 和 `labels` 怎么配对？
- [ ] Service 的 `port` 和 `targetPort` 区别？
- [ ] Pod 重启了 IP 变了，别的服务怎么还能找到它？
- [ ] `livenessProbe` 和 `readinessProbe` 有什么区别？
- [ ] 怎么让集群内 Pod 访问集群外的 Ollama？
- [ ] ServiceMonitor 的三个关键 label/selector 是什么？
- [ ] HPA 怎么拿到 p99_latency 这个指标？（四层链路）
- [ ] `deploy.sh` 里面为什么有那么多 `patch_*` 函数？
