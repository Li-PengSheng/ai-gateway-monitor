package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"google.golang.org/grpc"

	"my-go-gateway/config"
	modelv1 "my-go-gateway/gen/model/v1"
)

type mockModelClient struct{}

func (m *mockModelClient) ModelPredict(
	ctx context.Context,
	in *modelv1.ModelPredictRequest,
	opts ...grpc.CallOption,
) (*modelv1.ModelPredictResponse, error) {
	return &modelv1.ModelPredictResponse{Response: "ok", ModelName: "test"}, nil
}

func (m *mockModelClient) ModelPredictStream(
	ctx context.Context,
	in *modelv1.ModelPredictRequest,
	opts ...grpc.CallOption,
) (grpc.ServerStreamingClient[modelv1.ModelPredictResponse], error) {
	return nil, nil
}

func setupModelRouter(t *testing.T, client modelv1.ModelPredictorClient, maxPromptLen int) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	cfg := config.Load()
	cfg.MaxPromptLen = maxPromptLen
	r.POST("/predict/model", NewModelHandler(client, cfg).Predict)
	return r
}

func TestModelPredictMissingPrompt(t *testing.T) {
	r := setupModelRouter(t, &mockModelClient{}, 2000)

	req := httptest.NewRequest(http.MethodPost, "/predict/model", bytes.NewBufferString(`{}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}

	var apiErr APIError
	if err := json.Unmarshal(w.Body.Bytes(), &apiErr); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if !strings.Contains(apiErr.Message, "Prompt") {
		t.Fatalf("message = %q, want validation message about Prompt", apiErr.Message)
	}
	if apiErr.Code != CodeInvalidArgument {
		t.Fatalf("code = %q, want %q", apiErr.Code, CodeInvalidArgument)
	}
	if apiErr.RequestID == "" {
		t.Fatal("expected non-empty request_id")
	}
}

func TestModelPredictPromptTooLong(t *testing.T) {
	const maxLen = 10
	r := setupModelRouter(t, &mockModelClient{}, maxLen)

	payload := fmt.Sprintf(`{"prompt":"%s"}`, strings.Repeat("a", maxLen+1))
	req := httptest.NewRequest(http.MethodPost, "/predict/model", bytes.NewBufferString(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}

	want := fmt.Sprintf("prompt exceeds maximum length of %d characters", maxLen)
	var apiErr APIError
	if err := json.Unmarshal(w.Body.Bytes(), &apiErr); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if apiErr.Message != want {
		t.Fatalf("message = %q, want %q", apiErr.Message, want)
	}
	if apiErr.Code != CodeInvalidArgument {
		t.Fatalf("code = %q, want %q", apiErr.Code, CodeInvalidArgument)
	}
	if apiErr.RequestID == "" {
		t.Fatal("expected non-empty request_id")
	}
}

func TestModelPredictPromptLengthCountsRunes(t *testing.T) {
	// 10 Chinese characters = 30 bytes; with a 10-char limit this must pass,
	// which fails if the handler measures bytes instead of runes.
	const maxLen = 10
	r := setupModelRouter(t, &mockModelClient{}, maxLen)

	payload := fmt.Sprintf(`{"prompt":"%s"}`, strings.Repeat("语", maxLen))
	req := httptest.NewRequest(http.MethodPost, "/predict/model", bytes.NewBufferString(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", w.Code, http.StatusOK, w.Body.String())
	}
}

func TestModelPredictInvalidJSON(t *testing.T) {
	r := setupModelRouter(t, &mockModelClient{}, 2000)

	req := httptest.NewRequest(http.MethodPost, "/predict/model", bytes.NewBufferString(`not-json`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}
