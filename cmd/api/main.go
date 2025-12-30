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

	"sybil-api/internal/middleware"
	"sybil-api/internal/routers"
	"sybil-api/internal/shared"

	_ "github.com/go-sql-driver/mysql"
	"github.com/labstack/echo/v4"
	emw "github.com/labstack/echo/v4/middleware"
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
	debug := flag.Bool("debug", false, "Debug enabled")
	targonAPIKey := flag.String("targon-api-key", "", "Targon API Key")
	targonEndpoint := flag.String("targon-endpoint", "", "Targon endpoint")

	googleSearchEngineID := flag.String("google-search-engine-id", "", "Google search engine id")
	googleAPIKey := flag.String("google-api-key", "", "Google search api key")
	googleACURL := flag.String("google-ac-url", "", "Google AC URL")

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

	e := echo.New()
	e.GET(("/ping"), func(c echo.Context) error {
		return c.String(200, "")
	})
	e.GET("/metrics", echo.WrapHandler(promhttp.Handler()), func(next echo.HandlerFunc) echo.HandlerFunc {
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
	base := e.Group("")
	base.Use(emw.CORS())
	base.Use(middleware.NewRecoverMiddleware(log))
	base.Use(middleware.NewTrackMiddleware(log))

	middleware.InitUserMiddleware(redisClient, readDB, log)

	// Register routes
	err = routers.RegisterAdminRoutes(base, writeDB, readDB, redisClient, *targonAPIKey, *targonEndpoint, log)
	if err != nil {
		panic(err)
	}
	shutdown, err := routers.RegisterInferenceRoutes(base, writeDB, readDB, redisClient, log, *debug)
	if err != nil {
		panic(err)
	}
	defer shutdown()

	if *googleSearchEngineID != "" && *googleAPIKey != "" {
		err := routers.RegisterSearchRoutes(base, routers.SearchRouterConfig{
			GoogleSearchEngineID: *googleSearchEngineID,
			GoogleAPIKey:         *googleAPIKey,
			GoogleACURL:          *googleACURL,
		})
		if err != nil {
			panic(err)
		}
		log.Info("Search routes registered")
	}

	go func() {
		if err := e.Start(":80"); err != nil && err != http.ErrServerClosed {
			e.Logger.Fatal("shutting down the server")
		}
	}()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// Wait for interrupt signal to gracefully shut down the server with a timeout of 10 seconds.
	<-ctx.Done()

	ctx, cancel := context.WithTimeout(context.Background(), shared.DefaultShutdownTimeout)
	defer cancel()
	if err := e.Shutdown(ctx); err != nil {
		e.Logger.Fatal(err)
	}
}
