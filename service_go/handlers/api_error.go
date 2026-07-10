package handlers

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"my-go-gateway/metrics"
)

const (
	CodeModelTimeout       = "MODEL_TIMEOUT"
	CodeBackendUnavailable = "BACKEND_UNAVAILABLE"
	CodeInvalidArgument    = "INVALID_ARGUMENT"
	CodeInternalError      = "INTERNAL_ERROR"
	CodeStreamError        = "STREAM_ERROR"
)

// APIError is the go-gateway HTTP/JSON error contract. gRPC status codes from
// python-ai are translated at this boundary only.
type APIError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id"`
}

func APIErrorFromGRPC(err error) (APIError, int) {
	st, ok := status.FromError(err)
	if !ok {
		return APIError{
			Code:    CodeInternalError,
			Message: err.Error(),
		}, http.StatusInternalServerError
	}

	switch st.Code() {
	case codes.DeadlineExceeded:
		return APIError{
			Code:    CodeModelTimeout,
			Message: st.Message(),
		}, http.StatusGatewayTimeout
	case codes.Unavailable:
		return APIError{
			Code:    CodeBackendUnavailable,
			Message: st.Message(),
		}, http.StatusServiceUnavailable
	case codes.InvalidArgument:
		return APIError{
			Code:    CodeInvalidArgument,
			Message: st.Message(),
		}, http.StatusBadRequest
	default:
		return APIError{
			Code:    CodeInternalError,
			Message: st.Message(),
		}, http.StatusInternalServerError
	}
}

func writeGRPCError(c *gin.Context, path string, err error) {
	apiErr, httpStatus := APIErrorFromGRPC(err)
	apiErr.RequestID = requestID(c)
	metrics.HTTPRequestsTotal.WithLabelValues(path, strconv.Itoa(httpStatus)).Inc()
	c.JSON(httpStatus, apiErr)
}

func writeValidationError(c *gin.Context, path string, message string) {
	metrics.HTTPRequestsTotal.WithLabelValues(path, "400").Inc()
	c.JSON(http.StatusBadRequest, APIError{
		Code:      CodeInvalidArgument,
		Message:   message,
		RequestID: requestID(c),
	})
}

func requestID(c *gin.Context) string {
	if id := c.GetHeader("X-Request-ID"); id != "" {
		return id
	}
	return uuid.New().String()
}
