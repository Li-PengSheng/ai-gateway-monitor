// Package grpcclient dials the python-ai gRPC backend with OTel and keepalive.
package grpcclient

import (
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"

	"my-go-gateway/config"
)

// Dial creates a lazy gRPC client connection to cfg.AIServiceAddr.
//
// Parameters:
//   - cfg: must provide AIServiceAddr, keepalive timings, and MaxRecvMsgSize
//
// Returns a *grpc.ClientConn and a dial-option error. The connection is not
// fully established until Connect() or the first RPC (see handlers.Readyz).
//
// Transport is insecure by design for this demo (cluster-internal plaintext).
// PermitWithoutStream is true so idle channels still ping; this must stay
// paired with python-ai SERVER_OPTIONS (min ping interval 20s, permit without
// calls) or the server responds with GOAWAY "too_many_pings".
//
// Side effects: none beyond constructing the ClientConn; call Close when done.
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
