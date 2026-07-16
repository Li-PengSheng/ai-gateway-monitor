# Stage 1: compile the gateway binary
FROM golang:1.25-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

# Source includes checked-in generated protobuf stubs under gen/
COPY . .

RUN go build -o gateway main.go

# Stage 2: minimal runtime image
FROM alpine:3.20

RUN addgroup -S gateway && adduser -S gateway -G gateway

WORKDIR /home/gateway
COPY --from=builder --chown=gateway:gateway /app/gateway .

USER gateway

EXPOSE 8080

CMD ["./gateway"]
