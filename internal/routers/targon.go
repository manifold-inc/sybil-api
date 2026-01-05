package routers

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"sybil-api/internal/ctx"
	"sybil-api/internal/handlers/targon"
	"sybil-api/internal/shared"

	"github.com/labstack/echo/v4"
)

type TargonRouter struct {
	th *targon.TargonHandler
}

func NewTargonRouter(th *targon.TargonHandler) *TargonRouter {
	return &TargonRouter{th: th}
}

func (tr *TargonRouter) CreateModel(cc echo.Context) error {
	c := cc.(*ctx.Context)

	// Read and parse request body
	body, err := io.ReadAll(c.Request().Body)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, shared.ErrInternalServerError)
	}

	var req targon.CreateModelRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid JSON format"})
	}

	// Call business logic with structured input
	output, err := tr.th.CreateModelLogic(targon.CreateModelInput{
		Ctx:    c.Request().Context(),
		UserID: c.User.UserID,
		Req:    req,
	})
	// Handle errors
	if err != nil {
		c.LogValues.AddError(err)
		switch true {
		case errors.Is(err, shared.ErrBadRequest):
			return c.JSON(shared.ErrBadRequest.StatusCode, map[string]string{"error": shared.ErrBadRequest.Error()})
		default:
			return c.JSON(shared.ErrInternalServerError.StatusCode, map[string]string{"error": shared.ErrInternalServerError.Error()})
		}
	}

	// Return success response
	return c.JSON(http.StatusOK, map[string]any{
		"model_id":   output.ModelID,
		"targon_uid": output.TargonUID,
		"name":       output.Name,
		"status":     output.Status,
		"message":    output.Message,
	})
}

func (tr *TargonRouter) DeleteModel(cc echo.Context) error {
	c := cc.(*ctx.Context)

	output, err := tr.th.DeleteModelLogic(targon.DeleteModelInput{
		Ctx:      c.Request().Context(),
		UserID:   c.User.UserID,
		ModelUID: c.Param("uid"),
	})
	// Handle errors
	if err != nil {
		c.LogValues.AddError(err)
		switch true {
		case errors.Is(err, shared.ErrNotFound):
			return c.JSON(shared.ErrNotFound.StatusCode, map[string]string{"error": "model not found"})
		default:
			return c.JSON(shared.ErrInternalServerError.StatusCode, map[string]string{"error": shared.ErrInternalServerError.Error()})
		}
	}

	return c.JSON(http.StatusOK, map[string]any{
		"message":     output.Message,
		"targon_uid":  output.TargonUID,
		"model_id":    output.ModelID,
		"model_names": output.ModelNames,
	})
}

func (tr *TargonRouter) UpdateModel(cc echo.Context) error {
	c := cc.(*ctx.Context)

	body, err := io.ReadAll(c.Request().Body)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, shared.ErrInternalServerError)
	}

	var req targon.UpdateModelRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid JSON format"})
	}

	output, err := tr.th.UpdateModelLogic(targon.UpdateModelInput{
		Ctx:    c.Request().Context(),
		UserID: c.User.UserID,
		Req:    req,
	})
	// Handle errors
	if err != nil {
		c.LogValues.AddError(err)
		switch true {
		case errors.Is(err, shared.ErrNotFound):
			return c.JSON(shared.ErrNotFound.StatusCode, map[string]string{"error": "model not found"})
		case errors.Is(err, shared.ErrBadRequest):
			return c.JSON(shared.ErrBadRequest.StatusCode, map[string]string{"error": shared.ErrBadRequest.Error()})
		case errors.Is(err, shared.ErrPartialSuccess):
			return c.JSON(shared.ErrPartialSuccess.StatusCode, map[string]string{"error": "partial success; resource may be in unknown state"})
		default:
			return c.JSON(shared.ErrInternalServerError.StatusCode, map[string]string{"error": shared.ErrInternalServerError.Error()})
		}
	}

	response := map[string]any{
		"message":    output.Message,
		"targon_uid": output.TargonUID,
		"model_id":   output.ModelID,
	}
	if output.Name != nil {
		response["name"] = *output.Name
	}

	return c.JSON(http.StatusOK, response)
}

// RegisterTargonRoutes registers all targon routes
func RegisterTargonRoutes(e *echo.Group, th *targon.TargonHandler) {
	tr := NewTargonRouter(th)

	e.POST("/model", tr.CreateModel)
	e.DELETE("/model/:uid", tr.DeleteModel)
	e.PATCH("/model", tr.UpdateModel)
}
