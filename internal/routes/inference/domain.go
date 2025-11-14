package inference

import (
	"context"

	"go.uber.org/zap"
)

type Invocation struct {
	Ctx            context.Context
	UserID         uint64
	Credits        uint64
	PlanRequests   uint
	AllowOverspend bool
	RequestID      string
	Endpoint       string // ["chat/completions", "embeddings", "responses"]
	Model          string // will be extracted from Body in Preprocess
	Stream         bool   // Can be false initially, will be set in Preprocess
	Body           []byte
	Metadata       map[string]any 
	Log            *zap.SugaredLogger
}

type Responder interface {
	SendJSON(status int, v any) error 
	SendChunk(data []byte) error 
	SendError(err error) error 
	SetHeader(key, value string) 
	Flush() error 
}