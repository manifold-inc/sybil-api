package routers

import (
	"encoding/json"
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
	output := tr.th.CreateModelLogic(targon.CreateModelInput{
		Ctx:    c.Request().Context(),
		UserID: c.User.UserID,
		Req:    req,
	})

	// Handle errors
	if output.Error != nil {
		return c.JSON(output.StatusCode, map[string]string{"error": output.Error.Error()})
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

	output := tr.th.DeleteModelLogic(targon.DeleteModelInput{
		Ctx:      c.Request().Context(),
		UserID:   c.User.UserID,
		ModelUID: c.Param("uid"),
	})

	if output.Error != nil {
		return c.JSON(output.StatusCode, map[string]string{"error": output.Error.Error()})
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

	output := tr.th.UpdateModelLogic(targon.UpdateModelInput{
		Ctx:    c.Request().Context(),
		UserID: c.User.UserID,
		Req:    req,
	})

	if output.Error != nil {
		return c.JSON(output.StatusCode, map[string]string{"error": output.Error.Error()})
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
