package handlers

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/status"
)

func TestAPIErrorFromGRPC(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantCode   string
		wantStatus int
	}{
		{
			name:       "deadline exceeded",
			err:        status.Error(codes.DeadlineExceeded, "context deadline exceeded"),
			wantCode:   CodeModelTimeout,
			wantStatus: http.StatusGatewayTimeout,
		},
		{
			name:       "unavailable",
			err:        status.Error(codes.Unavailable, "connection refused"),
			wantCode:   CodeBackendUnavailable,
			wantStatus: http.StatusServiceUnavailable,
		},
		{
			name:       "invalid argument",
			err:        status.Error(codes.InvalidArgument, "bad prompt"),
			wantCode:   CodeInvalidArgument,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "internal",
			err:        status.Error(codes.Internal, "boom"),
			wantCode:   CodeInternalError,
			wantStatus: http.StatusInternalServerError,
		},
		{
			name:       "non grpc error",
			err:        errors.New("plain error"),
			wantCode:   CodeInternalError,
			wantStatus: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			apiErr, httpStatus := APIErrorFromGRPC(tt.err)
			if apiErr.Code != tt.wantCode {
				t.Fatalf("code = %q, want %q", apiErr.Code, tt.wantCode)
			}
			if httpStatus != tt.wantStatus {
				t.Fatalf("status = %d, want %d", httpStatus, tt.wantStatus)
			}
			if apiErr.Message == "" {
				t.Fatal("expected non-empty message")
			}
		})
	}
}

func setupHealthRouter(conn ConnStateProvider) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := NewHealthHandler(conn)
	r.GET("/healthz", h.Check)
	r.GET("/readyz", h.Readyz)
	return r
}

func TestHealthzAlwaysOK(t *testing.T) {
	r := setupHealthRouter(nil)

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestReadyzConnStates(t *testing.T) {
	tests := []struct {
		name        string
		state       connectivity.State
		wantStatus  int
		wantConnect bool
	}{
		// Idle means "never dialed" (grpc.NewClient is lazy) — the backend may
		// be down, so it must not count as ready; readyz kicks off a dial.
		{name: "idle", state: connectivity.Idle, wantStatus: http.StatusServiceUnavailable, wantConnect: true},
		{name: "connecting", state: connectivity.Connecting, wantStatus: http.StatusServiceUnavailable},
		{name: "ready", state: connectivity.Ready, wantStatus: http.StatusOK},
		{name: "transient failure", state: connectivity.TransientFailure, wantStatus: http.StatusServiceUnavailable},
		{name: "shutdown", state: connectivity.Shutdown, wantStatus: http.StatusServiceUnavailable},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn := &mockConn{state: tt.state}
			r := setupHealthRouter(conn)

			req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body = %s", w.Code, tt.wantStatus, w.Body.String())
			}
			if conn.connectCalled != tt.wantConnect {
				t.Fatalf("connectCalled = %v, want %v", conn.connectCalled, tt.wantConnect)
			}
		})
	}
}

func TestReadyzNilConn(t *testing.T) {
	r := setupHealthRouter(nil)

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
}

type mockConn struct {
	state         connectivity.State
	connectCalled bool
}

func (m *mockConn) GetState() connectivity.State {
	return m.state
}

func (m *mockConn) Connect() {
	m.connectCalled = true
}
