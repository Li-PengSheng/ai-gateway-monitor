package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"my-go-gateway/config"
	irisv1 "my-go-gateway/gen/iris/v1"
)

type mockIrisClient struct{}

func (m *mockIrisClient) IrisPredict(
	ctx context.Context,
	in *irisv1.IrisPredictRequest,
	opts ...grpc.CallOption,
) (*irisv1.IrisPredictResponse, error) {
	return &irisv1.IrisPredictResponse{ClassName: "setosa", ClassId: 0}, nil
}

func setupIrisRouter(t *testing.T, client irisv1.IrisPredictorClient) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	cfg := config.Load()
	r.POST("/predict/iris", NewIrisHandler(client, cfg).Predict)
	return r
}

func assertValidationAPIError(t *testing.T, w *httptest.ResponseRecorder, wantMessageContains string) {
	t.Helper()

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}

	var apiErr APIError
	if err := json.Unmarshal(w.Body.Bytes(), &apiErr); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if apiErr.Code != CodeInvalidArgument {
		t.Fatalf("code = %q, want %q", apiErr.Code, CodeInvalidArgument)
	}
	if apiErr.Message == "" {
		t.Fatal("expected non-empty message")
	}
	if wantMessageContains != "" && !strings.Contains(apiErr.Message, wantMessageContains) {
		t.Fatalf("message = %q, want substring %q", apiErr.Message, wantMessageContains)
	}
	if apiErr.RequestID == "" {
		t.Fatal("expected non-empty request_id")
	}
}

func TestIrisPredictInvalidJSON(t *testing.T) {
	r := setupIrisRouter(t, &mockIrisClient{})

	req := httptest.NewRequest(http.MethodPost, "/predict/iris", bytes.NewBufferString(`not-json`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assertValidationAPIError(t, w, "")
}

func TestIrisPredictMissingBody(t *testing.T) {
	r := setupIrisRouter(t, &mockIrisClient{})

	req := httptest.NewRequest(http.MethodPost, "/predict/iris", nil)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestIrisPredictInvalidFieldType(t *testing.T) {
	r := setupIrisRouter(t, &mockIrisClient{})

	req := httptest.NewRequest(http.MethodPost, "/predict/iris", bytes.NewBufferString(`{"sepal_length":"bad"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestIrisPredictMissingFields(t *testing.T) {
	r := setupIrisRouter(t, &mockIrisClient{})

	req := httptest.NewRequest(http.MethodPost, "/predict/iris", bytes.NewBufferString(`{}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assertValidationAPIError(t, w, "all iris features are required")
}

func TestIrisPredictAllZeroFieldsAccepted(t *testing.T) {
	r := setupIrisRouter(t, &mockIrisClient{})

	payload := `{"sepal_length":0,"sepal_width":0,"petal_length":0,"petal_width":0}`
	req := httptest.NewRequest(http.MethodPost, "/predict/iris", bytes.NewBufferString(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", w.Code, http.StatusOK, w.Body.String())
	}
}

type mockIrisClientDeadline struct{}

func (m *mockIrisClientDeadline) IrisPredict(
	ctx context.Context,
	in *irisv1.IrisPredictRequest,
	opts ...grpc.CallOption,
) (*irisv1.IrisPredictResponse, error) {
	return nil, status.Error(codes.DeadlineExceeded, "context deadline exceeded")
}

func TestIrisPredictSuccessE2E(t *testing.T) {
	r := setupIrisRouter(t, &mockIrisClient{})

	payload := `{"sepal_length":5.1,"sepal_width":3.5,"petal_length":1.4,"petal_width":0.2}`
	req := httptest.NewRequest(http.MethodPost, "/predict/iris", bytes.NewBufferString(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", w.Code, http.StatusOK, w.Body.String())
	}

	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if body["result"] != "setosa" {
		t.Fatalf("result = %v, want setosa", body["result"])
	}
}

func TestIrisPredictTimeoutE2E(t *testing.T) {
	r := setupIrisRouter(t, &mockIrisClientDeadline{})

	payload := `{"sepal_length":5.1,"sepal_width":3.5,"petal_length":1.4,"petal_width":0.2}`
	req := httptest.NewRequest(http.MethodPost, "/predict/iris", bytes.NewBufferString(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, want %d; body = %s", w.Code, http.StatusGatewayTimeout, w.Body.String())
	}

	var apiErr APIError
	if err := json.Unmarshal(w.Body.Bytes(), &apiErr); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if apiErr.Code != CodeModelTimeout {
		t.Fatalf("code = %q, want %q", apiErr.Code, CodeModelTimeout)
	}
}
