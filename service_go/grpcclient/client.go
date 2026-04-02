package grpcclient

import (
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"

	"my-go-gateway/config"
)

func Dial(cfg *config.Config) (*grpc.ClientConn, error) {
	return grpc.NewClient(
		cfg.AIServiceAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(cfg.GRPCMaxRecvMsgSize),
		),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                cfg.GRPCKeepAliveTime,
			Timeout:             cfg.GRPCKeepAliveTimeout,
			PermitWithoutStream: true,
		}),
	)
}