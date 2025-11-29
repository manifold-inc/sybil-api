package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"sybil-api/internal/metrics"
	auth "sybil-api/internal/middleware"
	"sybil-api/internal/routes/inference"
	"sybil-api/internal/routes/search"
	"sybil-api/internal/routes/targon"
	"sybil-api/internal/setup"
	"sybil-api/internal/shared"

	"github.com/aidarkhanov/nanoid"
	_ "github.com/go-sql-driver/mysql"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/manifold-inc/manifold-sdk/lib/eflag"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func main() {
	// Flags / ENV Variables
	writeDSN := flag.String("dsn", "", "Write vitess DSN")
	readDSN := flag.String("read-dsn", "", "Write vitess DSN")
	metricsAPIKey := flag.String("metrics-api-key", "", "Metrics api key")
	redisAddr := flag.String("redis-addr", "", "Redis host:port")
	googleSearchEngineID := flag.String("google-search-engine-id", "", "Google search engine id")
	googleAPIKey := flag.String("google-api-key", "", "Google search api key")
	googleACURL := flag.String("google-ac-url", "", "Google AC URL")
	debug := flag.Bool("debug", false, "Debug enabled")

	err := eflag.SetFlagsFromEnvironment()
	if err != nil {
		panic(err)
	}
	flag.Parse()

	// Write DB init
	writeDB, err := sql.Open("mysql", *writeDSN)
	if err != nil {
		panic(fmt.Sprintf("failed initializing sqlClient: %s", err))
	}
	err = writeDB.Ping()
	if err != nil {
		panic(fmt.Sprintf("failed ping to sql db: %s", err))
	}

	// Read db init
	readDB, err := sql.Open("mysql", *readDSN)
	if err != nil {
		panic(fmt.Sprintf("failed initializing readSqlClient: %s", err))
	}
	err = readDB.Ping()
	if err != nil {
		panic(fmt.Sprintf("failed to ping read replica sql db: %s", err))
	}

	// Load Redis connection
	redisClient := redis.NewClient(&redis.Options{
		Addr:     *redisAddr,
		Password: "",
		DB:       0,
	})
	if err := redisClient.Ping(context.Background()).Err(); err != nil {
		panic(fmt.Sprintf("failed ping to redis db: %s", err))
	}

	defer func() {
		if redisClient != nil {
			_ = redisClient.Close()
		}
		if writeDB != nil {
			_ = writeDB.Close()
		}
		if readDB != nil {
			_ = readDB.Close()
		}
	}()

	var logger *zap.Logger
	if !*debug {
		logger, err = zap.NewProduction()
		if err != nil {
			panic("Failed init logger")
		}
	}
	if *debug {
		logger, err = zap.NewDevelopment()
		if err != nil {
			panic("Failed init logger")
		}
	}
	log := logger.Sugar()

	server := echo.New()
	e := server.Group("")
	e.Use(middleware.CORS())
	e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			reqID, _ := nanoid.Generate("0123456789abcdefghijklmnopqrstuvwxyz", 28)
			logger := log.With(
				"request_id", "req_"+reqID,
			)
			logger = logger.With("externalid", c.Request().Header.Get("X-Dippy-Request-Id"))

			cc := &setup.Context{Context: c, Log: logger, Reqid: reqID}
			start := time.Now()
			err := next(cc)
			duration := time.Since(start)
			cc.Log.Infow("end_of_request", "status_code", fmt.Sprintf("%d", cc.Response().Status), "duration", duration.String())
			metrics.ResponseCodes.WithLabelValues(cc.Path(), fmt.Sprintf("%d", cc.Response().Status)).Inc()
			return err
		}
	})
	e.Use(middleware.RecoverWithConfig(middleware.RecoverConfig{
		StackSize: 1 << 10, // 1 KB
		LogErrorFunc: func(c echo.Context, err error, stack []byte) error {
			defer func() {
				_ = log.Sync()
			}()
			log.Errorw("Api Panic", "error", err.Error())
			return c.String(500, shared.ErrInternalServerError.Err.Error())
		},
	}))

	e.GET(("/ping"), func(c echo.Context) error {
		return c.String(200, "")
	})

	userManager := auth.NewUserManager(redisClient, readDB, log.With("manager", "user_manager"))
	withUser := e.Group("", userManager.ExtractUser)
	requiredUser := withUser.Group("", userManager.RequireUser)

	inferenceGroup := requiredUser.Group("/v1")
	inferenceManager, inferenceErr := inference.NewInferenceManager(writeDB, readDB, redisClient, log, *debug)
	if inferenceErr != nil {
		panic(inferenceErr)
	}
	defer inferenceManager.ShutDown()

	inferenceGroup.GET("/models", inferenceManager.Models)
	inferenceGroup.POST("/chat/completions", inferenceManager.ChatRequest)
	inferenceGroup.POST("/completions", inferenceManager.CompletionRequest)
	inferenceGroup.POST("/embeddings", inferenceManager.EmbeddingRequest)
	inferenceGroup.POST("/responses", inferenceManager.ResponsesRequest)
	inferenceGroup.POST("/chat/history/new", inferenceManager.CompletionRequestNewHistory)
	inferenceGroup.PATCH("/chat/history/:history_id", inferenceManager.UpdateHistory)

	searchGroup := requiredUser.Group("/search")
	searchManager, err := search.NewSearchManager(inferenceManager.HandleInferenceHTTP, *googleSearchEngineID, *googleAPIKey, *googleACURL)
	if err != nil {
		panic(err)
	}

	searchGroup.POST("/images", searchManager.GetImages)
	searchGroup.POST("", searchManager.Search)
	searchGroup.GET("/autocomplete", searchManager.GetAutocomplete)
	searchGroup.POST("/sources", searchManager.GetSources)

	requiredAdmin := requiredUser.Group("", userManager.RequireAdmin)
	targonGroup := requiredAdmin.Group("/models")
	targonManager, targonErr := targon.NewTargonManager(writeDB, readDB, redisClient, log)
	if targonErr != nil {
		panic(targonErr)
	}
	targonGroup.POST("", targonManager.CreateModel)
	targonGroup.DELETE("/:uid", targonManager.DeleteModel)
	targonGroup.PATCH("", targonManager.UpdateModel)

	metricsGroup := server.Group("/metrics")
	metricsGroup.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			apiKey, err := shared.ExtractAPIKey(c)
			if err != nil {
				return c.String(401, "Missing or invalid API key")
			}

			if apiKey != *metricsAPIKey {
				return c.String(401, "Unauthorized API key")
			}
			return next(c)
		}
	})
	metricsGroup.GET("", echo.WrapHandler(promhttp.Handler()))

	go func() {
		if err := server.Start(":80"); err != nil && err != http.ErrServerClosed {
			server.Logger.Fatal("shutting down the server")
		}
	}()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// Wait for interrupt signal to gracefully shut down the server with a timeout of 10 seconds.
	<-ctx.Done()

	ctx, cancel := context.WithTimeout(context.Background(), shared.DefaultShutdownTimeout)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		server.Logger.Fatal(err)
	}
}
