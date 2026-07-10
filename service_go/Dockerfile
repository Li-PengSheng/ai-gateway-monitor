# 第一阶段：编译
FROM golang:1.25-alpine AS builder

WORKDIR /app

# 复制依赖文件
COPY go.mod go.sum ./
RUN go mod download

# 复制源码（包含生成的 pb 代码）
COPY . .

# 编译成名为 'gateway' 的二进制文件
RUN go build -o gateway main.go

# 第二阶段：运行
FROM alpine:3.20

RUN addgroup -S gateway && adduser -S gateway -G gateway

WORKDIR /home/gateway
COPY --from=builder --chown=gateway:gateway /app/gateway .

USER gateway

# 暴露 Gin 的 HTTP 端口
EXPOSE 8080

CMD ["./gateway"]
