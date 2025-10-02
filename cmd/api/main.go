package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	auth "sybil-api/internal/middleware"
	"sybil-api/internal/routes/inference"
	"sybil-api/internal/routes/search"
	"sybil-api/internal/setup"
	"sybil-api/internal/shared"

	"github.com/aidarkhanov/nanoid"
	_ "github.com/go-sql-driver/mysql"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
)

func main() {
	core, errs := setup.CreateCore()
	if errs != nil {
		panic(fmt.Sprintf("Failed creating core: %s", errs))
	}
	defer core.Shutdown()

	server := echo.New()
	e := server.Group("")
	e.Use(middleware.CORS())
	e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			reqID, _ := nanoid.Generate("0123456789abcdefghijklmnopqrstuvwxyz", 28)
			logger := core.Log.With(
				"request_id", "req_"+reqID,
			)

			cc := &setup.Context{Context: c, Log: logger, Reqid: reqID, Core: core}
			start := time.Now()
			err := next(cc)
			duration := time.Since(start)
			cc.Log.Infow("end_of_request", "status_code", fmt.Sprintf("%d", cc.Response().Status), "duration", duration.String())
			return err
		}
	})
	e.Use(middleware.RecoverWithConfig(middleware.RecoverConfig{
		StackSize: 1 << 10, // 1 KB
		LogErrorFunc: func(c echo.Context, err error, stack []byte) error {
			defer func() {
				_ = core.Log.Sync()
			}()
			core.Log.Errorw("Api Panic", "error", err.Error())
			return c.String(500, shared.ErrInternalServerError.Err.Error())
		},
	}))

	e.GET(("/ping"), func(c echo.Context) error {
		return c.String(200, "")
	})

	withUser := e.Group("", auth.ExtractUser)
	requiredUser := withUser.Group("", auth.RequireUser)

	searchGroup := requiredUser.Group("/search")
	searchManager, err := search.NewSearchManager()
	if err != nil {
		panic(err)
	}

	searchGroup.POST("/images", searchManager.GetImages)
	searchGroup.POST("/", searchManager.Search)
	searchGroup.GET("/autocomplete", searchManager.GetAutocomplete)
	searchGroup.POST("/sources", searchManager.GetSources)

	inferenceGroup := requiredUser.Group("/v1")
	inferenceManager, inferenceErr := inference.NewInferenceManager(core.WDB, core.RDB, core.RedisClient, core.Log, core.Client, core.Debug)
	if inferenceErr != nil {
		panic(inferenceErr)
	}

	inferenceGroup.POST("/chat", inferenceManager.ChatRequest)
	inferenceGroup.POST("/completions", inferenceManager.CompletionRequest)

	// Start background sync for inference services
	go inferenceManager.StartBackgroundSync(30 * time.Second)

	go func() {
		if err := server.Start(":80"); err != nil && err != http.ErrServerClosed {
			server.Logger.Fatal("shutting down the server")
		}
	}()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// Wait for interrupt signal to gracefully shut down the server with a timeout of 10 seconds.
	<-ctx.Done()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		server.Logger.Fatal(err)
	}
}
