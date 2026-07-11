// service_go/handlers/health.go
package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"google.golang.org/grpc/connectivity"
)

// ConnStateProvider exposes gRPC connection state for readiness checks.
type ConnStateProvider interface {
	GetState() connectivity.State
	// Connect kicks an Idle channel into dialing (grpc.NewClient is lazy).
	Connect()
}

type HealthHandler struct {
	conn ConnStateProvider
}

func NewHealthHandler(conn ConnStateProvider) *HealthHandler {
	return &HealthHandler{conn: conn}
}

// Check is the liveness probe: process is up, no downstream dependency.
func (h *HealthHandler) Check(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// Readyz is the readiness probe: gRPC backend connection is usable.
func (h *HealthHandler) Readyz(c *gin.Context) {
	if h.conn == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"status": "not ready",
			"reason": "grpc connection not configured",
		})
		return
	}

	// grpc.NewClient dials lazily: the channel sits in Idle until the first
	// RPC, so Idle says nothing about whether the backend is reachable.
	// Trigger a connection attempt and only report ready once it succeeds.
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
