// Package handlers implements go-gateway HTTP endpoints: probes, Iris/Model
// predict APIs, and gRPC-to-HTTP error translation.
package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"google.golang.org/grpc/connectivity"
)

// ConnStateProvider exposes gRPC connection state for readiness checks.
// Typically backed by *grpc.ClientConn.
type ConnStateProvider interface {
	// GetState returns the channel's current connectivity state without blocking.
	GetState() connectivity.State
	// Connect kicks an Idle channel into dialing (grpc.NewClient is lazy).
	Connect()
}

// HealthHandler serves Kubernetes-style liveness (/healthz) and readiness
// (/readyz) probes for the go-gateway process.
type HealthHandler struct {
	conn ConnStateProvider
}

// NewHealthHandler returns a HealthHandler that reports readiness from conn.
// A nil conn makes Readyz always return 503.
func NewHealthHandler(conn ConnStateProvider) *HealthHandler {
	return &HealthHandler{conn: conn}
}

// Check is the liveness probe: process is up, no downstream dependency.
//
// Always responds 200 {"status":"ok"}. Side effects: none.
func (h *HealthHandler) Check(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// Readyz is the readiness probe: the gRPC backend channel must already be Ready.
//
// Behavior:
//   - nil conn → 503 with reason "grpc connection not configured"
//   - state Idle → calls Connect() to start dialing, then still evaluates the
//     pre-Connect state (Idle), so this request returns 503; a later probe may
//     observe Ready/Connecting/TransientFailure once the dial progresses
//   - state != Ready (including Connecting, TransientFailure, Shutdown) → 503
//   - state Ready → 200 {"status":"ready","grpc_state":"READY"}
//
// Why not re-read GetState() after Connect: Connect is asynchronous; a single
// probe cannot wait for dial completion without blocking the request. Readiness
// is therefore eventually consistent across successive kubelet polls.
//
// Side effects: may call conn.Connect() when the channel is Idle.
func (h *HealthHandler) Readyz(c *gin.Context) {
	if h.conn == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"status": "not ready",
			"reason": "grpc connection not configured",
		})
		return
	}

	state := h.conn.GetState()
	if state == connectivity.Idle {
		h.conn.Connect()
	}
	if state != connectivity.Ready {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"status":     "not ready",
			"grpc_state": state.String(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":     "ready",
		"grpc_state": state.String(),
	})
}
