import os
import sys
import time
from concurrent import futures

# --- 关键：解决 Import 路径问题 ---
# 把 'gen' 目录加入到 Python 搜索路径，这样就能直接 import 生成的代码了
sys.path.append(os.path.join(os.path.dirname(__file__), "gen"))
import grpc
import ollama
from iris.v1 import iris_pb2, iris_pb2_grpc  # ← fixed
from model.v1 import model_pb2, model_pb2_grpc  # ← fixed
from opentelemetry import trace
from opentelemetry.exporter.otlp.proto.grpc.trace_exporter import OTLPSpanExporter
from opentelemetry.instrumentation.grpc import GrpcInstrumentorServer
from opentelemetry.propagate import set_global_textmap  # 修复导入
from opentelemetry.sdk.resources import Resource
from opentelemetry.sdk.trace import TracerProvider
from opentelemetry.sdk.trace.export import BatchSpanProcessor
from opentelemetry.trace.propagation.tracecontext import (
    TraceContextTextMapPropagator,  # 修复类名
)
from sklearn.datasets import load_iris
from sklearn.ensemble import RandomForestClassifier

# 1. 初始化追踪器，发送到 Jaeger
resource = Resource.create({"service.name": "python-ai"})
provider = TracerProvider(resource=resource)
jaeger_endpoint = os.getenv("JAEGER_ENDPOINT", "localhost:4317")
processor = BatchSpanProcessor(OTLPSpanExporter(endpoint=jaeger_endpoint, insecure=True))
# processor = BatchSpanProcessor(OTLPSpanExporter(endpoint="jaeger:4317", insecure=True))
provider.add_span_processor(processor)
trace.set_tracer_provider(provider)

# 设置全局传播器，用于跨服务追踪上下文传递
set_global_textmap(TraceContextTextMapPropagator())

# 2. 自动拦截所有 gRPC 请求
instrumentor = GrpcInstrumentorServer()
instrumentor.instrument()

# 1. 模拟加载/训练模型 (实际项目中你会 load 一个 .pkl 文件)
iris = load_iris()
clf = RandomForestClassifier()
clf.fit(iris.data, iris.target)


class IrisPredictor(iris_pb2_grpc.IrisPredictorServicer):
    def IrisPredict(self, request, context):
        print(
            f"收到请求: sepal_len={request.sepal_length}, sepal_wid={request.sepal_width}..."
        )

        features = [
            [
                request.sepal_length,
                request.sepal_width,
                request.petal_length,
                request.petal_width,
            ]
        ]

        pred_idx = clf.predict(features)[0]
        class_name = iris.target_names[pred_idx]

        return iris_pb2.IrisPredictResponse(class_id=pred_idx, class_name=class_name)


# qwen model
class ModelPredictor(model_pb2_grpc.ModelPredictorServicer):
    def __init__(self):
        # 指向 WSL 宿主机上的 Ollama 服务
        host = os.getenv("OLLAMA_HOST", "http://localhost:11434")
        self.client = ollama.Client(host=host)

    def ModelPredict(self, request, context):
        print(f"收到提示词: {request.prompt}")
        response = self.client.generate(model="qwen2.5:1.5b", prompt=request.prompt)
        # 增加打印，看看字典里有没有 eval_count
        print(f"Eval Count: {response.eval_count}")

        return model_pb2.ModelPredictResponse(
            response=response.response,
            model_name="qwen2.5-1.5b",
            prompt_eval_count=response.prompt_eval_count or 0,
            eval_count=response.eval_count or 0,
            eval_duration=response.eval_duration or 0,
        )


def serve():
    server = grpc.server(futures.ThreadPoolExecutor(max_workers=10))
    iris_pb2_grpc.add_IrisPredictorServicer_to_server(IrisPredictor(), server)
    model_pb2_grpc.add_ModelPredictorServicer_to_server(
        ModelPredictor(), server
    )
    server.add_insecure_port("[::]:50051")
    print("Python AI Service (gRPC) is running on port 50051...")
    server.start()
    try:
        server.wait_for_termination()
    except KeyboardInterrupt:
        print("\nShutting down gracefully...")
        server.stop(grace=5)
        # 关闭追踪器
        provider.shutdown()
        print("Server stopped.")


if __name__ == "__main__":
    serve()

# curl -X POST http://localhost:8080/predict -H "Content-Type: application/json" -d '{"sepal_length": 6.0, "sepal_width": 3.0, "petal_length": 5.5, "petal_width": 2.0}'
