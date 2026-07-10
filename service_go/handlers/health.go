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

	state := h.conn.GetState()
	if state == connectivity.TransientFailure || state == connectivity.Shutdown {
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
