# 使用轻量级 Python 镜像
FROM python:3.12-slim

# 安装 uv
COPY --from=ghcr.io/astral-sh/uv:latest /uv /uvx /bin/

RUN useradd --create-home --uid 1000 appuser

# 设置工作目录
WORKDIR /app

# 复制依赖文件并安装
COPY pyproject.toml uv.lock ./
RUN uv sync --frozen && chown -R appuser:appuser /app

# 复制生成的代码和主程序
COPY --chown=appuser:appuser gen/ ./gen/
COPY --chown=appuser:appuser models/ ./models/
COPY --chown=appuser:appuser main.py observability.py server.py gpu_exporter.py ./

USER appuser

# 设置 PYTHONPATH 确保能找到 gen 目录
ENV PYTHONPATH="/app:/app/gen"

# 暴露 gRPC 端口
EXPOSE 50051

# 启动服务
CMD ["uv", "run", "main.py"]
