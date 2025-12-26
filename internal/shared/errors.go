package shared

import "errors"

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
)
