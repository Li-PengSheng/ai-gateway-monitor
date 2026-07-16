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

// Stable machine-readable codes in the go-gateway HTTP/JSON error body.
// Clients should branch on Code, not on Message text.
const (
	// CodeModelTimeout maps gRPC DeadlineExceeded → HTTP 504.
	CodeModelTimeout = "MODEL_TIMEOUT"
	// CodeBackendUnavailable maps gRPC Unavailable → HTTP 503.
	CodeBackendUnavailable = "BACKEND_UNAVAILABLE"
	// CodeInvalidArgument maps gRPC InvalidArgument (and local validation) → HTTP 400.
	CodeInvalidArgument = "INVALID_ARGUMENT"
	// CodeInternalError is the fallback for non-gRPC errors and unmapped gRPC codes
	// (including ResourceExhausted from python-ai concurrency limits) → HTTP 500.
	CodeInternalError = "INTERNAL_ERROR"
	// CodeStreamError is used in SSE "error" events when the gRPC stream fails
	// after headers were already flushed as HTTP 200.
	CodeStreamError = "STREAM_ERROR"
)

// APIError is the go-gateway HTTP/JSON error contract. gRPC status codes from
// python-ai are translated at this boundary only; handlers must not invent
// parallel error shapes.
type APIError struct {
	// Code is the stable machine-readable value clients should branch on.
	Code string `json:"code"`
	// Message is diagnostic text and is not a stable client contract.
	Message string `json:"message"`
	// RequestID correlates the response with the originating request chain.
	RequestID string `json:"request_id"`
}

// APIErrorFromGRPC maps a gRPC (or non-gRPC) error to the public APIError
// contract and the corresponding HTTP status code.
//
// Parameters:
//   - err: typically a status error from a gRPC unary/stream call; nil is not expected
//
// Returns:
//   - APIError with Code and Message set; RequestID is left empty for the caller to fill
//   - HTTP status to use in the response
//
// Mapping (intentional allow-list; everything else is Internal):
//
//	DeadlineExceeded  → MODEL_TIMEOUT / 504
//	Unavailable       → BACKEND_UNAVAILABLE / 503
//	InvalidArgument   → INVALID_ARGUMENT / 400
//	other gRPC codes  → INTERNAL_ERROR / 500
//	non-gRPC error    → INTERNAL_ERROR / 500 (Message = err.Error())
//
// Why ResourceExhausted is not special-cased: python-ai may return it when
// maximum_concurrent_rpcs is hit, but the gateway treats overload like other
// unexpected backend failures (500) rather than advertising a separate client
// retry contract. Change this mapping only together with API docs and clients.
//
// Side effects: none (pure function).
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

// writeGRPCError translates err, attaches a request ID, records the final HTTP
// status metric for path, and writes the JSON response. It commits the response
// and therefore must not be called after SSE headers have been flushed.
func writeGRPCError(c *gin.Context, path string, err error) {
	apiErr, httpStatus := APIErrorFromGRPC(err)
	apiErr.RequestID = requestID(c)
	metrics.HTTPRequestsTotal.WithLabelValues(path, strconv.Itoa(httpStatus)).Inc()
	c.JSON(httpStatus, apiErr)
}

// writeValidationError records a 400 for path and commits the standard
// INVALID_ARGUMENT JSON response with a correlated request ID.
func writeValidationError(c *gin.Context, path string, message string) {
	metrics.HTTPRequestsTotal.WithLabelValues(path, "400").Inc()
	c.JSON(http.StatusBadRequest, APIError{
		Code:      CodeInvalidArgument,
		Message:   message,
		RequestID: requestID(c),
	})
}

// requestID preserves a caller-supplied X-Request-ID so logs and responses can
// be correlated across an existing request chain; otherwise it creates a UUID.
// The value is intentionally passed through without validation at this layer,
// so an edge proxy must enforce any required length or character restrictions.
// Side effect: UUID generation when the request header is absent.
func requestID(c *gin.Context) string {
	if id := c.GetHeader("X-Request-ID"); id != "" {
		return id
	}
	return uuid.New().String()
}
