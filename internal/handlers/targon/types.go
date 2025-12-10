package targon

import "context"

// CreateModelInput contains all data needed for CreateModel business logic
type CreateModelInput struct {
	Ctx    context.Context
	UserID uint64
	Req    CreateModelRequest
}

type CreateModelOutput struct {
	ModelID    int64
	TargonUID  string
	Name       string
	Status     string
	Message    string
	Error      error
	StatusCode int
}

// DeleteModelInput contains all data needed for DeleteModel business logic
type DeleteModelInput struct {
	Ctx      context.Context
	UserID   uint64
	ModelUID string
}

type DeleteModelOutput struct {
	ModelID    uint64
	TargonUID  string
	ModelNames []string
	Message    string
	Error      error
	StatusCode int
}

// UpdateModelInput contains all data needed for UpdateModel business logic
type UpdateModelInput struct {
	Ctx    context.Context
	UserID uint64
	Req    UpdateModelRequest
}

type UpdateModelOutput struct {
	ModelID    uint64
	TargonUID  string
	Name       *string
	Message    string
	Error      error
	StatusCode int
}
