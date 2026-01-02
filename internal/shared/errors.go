package shared

import (
	"errors"
	"fmt"
)

// RequestError is used when we want a specific error message and StatusCode.
// sane defaults are listed below. For routes that need custom error messages,
// a request error can be generated and a handler expects the router to return
// the exact message inside the request error msg
//
// Error codes should be bubbled where the RequestError msg is expected to be
// returned to the user. If the user should see a generic error message but
// the error chain should include more detail for logging purposes, then a generic
// error should be added that provides context
type RequestError struct {
	StatusCode int
	Err        error
}

func (r *RequestError) Error() string {
	return fmt.Sprintf("status %d: err %v", r.StatusCode, r.Err)
}

var (
	ErrMissingAuth   = &RequestError{Err: errors.New("missing authorization header"), StatusCode: 401}
	ErrInvalidFormat = &RequestError{Err: errors.New("invalid authentication format"), StatusCode: 401}
	ErrInvalidKeyLen = &RequestError{Err: errors.New("invalid API key length"), StatusCode: 401}
	ErrUnauthorized  = &RequestError{Err: errors.New("unauthorized"), StatusCode: 401}

	ErrInvalidAPIKey  = &RequestError{Err: errors.New("invalid API key"), StatusCode: 400}
	ErrInvalidRequest = &RequestError{Err: errors.New("invalid request body"), StatusCode: 400}

	ErrKeyNotFound = &RequestError{Err: errors.New("key not found"), StatusCode: 404}
	ErrKeyInUse    = &RequestError{Err: errors.New("cannot delete key in use"), StatusCode: 403}
	ErrKeyRequired = &RequestError{Err: errors.New("key is required"), StatusCode: 400}

	ErrInternalServerError = &RequestError{Err: errors.New("internal server error"), StatusCode: 500}
	ErrBadRequest          = &RequestError{Err: errors.New("bad request"), StatusCode: 400}
	ErrNotFound            = &RequestError{Err: errors.New("not found"), StatusCode: 404}
	ErrPartialSuccess      = &RequestError{Err: errors.New("partial success"), StatusCode: 200}

	ErrColdStart              = &MetricsError{Msg: "model cold start", Code: "model_cold_start"}
	ErrFailedModelReq         = &MetricsError{Msg: "failed to send http request to model", Code: "model_http_err"}
	ErrFailedModelReqFromCode = &MetricsError{Msg: "model responded with non-200", Code: "model_http_status_err"}
	ErrFailedReadingResponse  = &MetricsError{Msg: "failed to read model response", Code: "model_response_err"}
	ErrMissingDoneToken       = &MetricsError{Msg: "missing [DONE] token", Code: "missing_done_token"}
	ErrModelContext           = &MetricsError{Msg: "model context canceled", Code: "model_context_err"}
)

type MetricsError struct {
	Msg  string
	Code string
}

func (m *MetricsError) Error() string {
	return m.String()
}

func (m *MetricsError) String() string {
	return m.Msg
}
