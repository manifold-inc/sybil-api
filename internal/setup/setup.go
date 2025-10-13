// Package setup server
package setup

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"

	"sybil-api/internal/shared"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

type Context struct {
	echo.Context
	Log   *zap.SugaredLogger
	Reqid string
	Core  *Core
	User  *shared.UserMetadata
}

type Core struct {
	Env         Environment
	RedisClient *redis.Client
	WDB         *sql.DB
	RDB         *sql.DB
	Log         *zap.SugaredLogger
	Debug       bool
}

type Environment struct {
	InstanceUUID  string
	MetricsAPIKey string
}

func (c *Core) Shutdown() {
	if c.RedisClient != nil {
		_ = c.RedisClient.Close()
	}
	if c.WDB != nil {
		_ = c.WDB.Close()
	}
	if c.RDB != nil {
		_ = c.RDB.Close()
	}
}

func CreateCore() (*Core, []error) {
	var errs []error

	DSN, err := shared.SafeEnv("DSN")
	if err != nil {
		errs = append(errs, err)
	}
	readDSN, err := shared.SafeEnv("READ_DSN")
	if err != nil {
		errs = append(errs, err)
	}

	metricsAPIKey, err := shared.SafeEnv("METRICS_API_KEY")
	if err != nil {
		errs = append(errs, err)
	}

	redisHost := shared.GetEnv("REDIS_HOST", "cache")
	redisPort := shared.GetEnv("REDIS_PORT", "6379")

	instanceUUID := uuid.New().String()
	DEBUG, err := strconv.ParseBool(shared.GetEnv("DEBUG", "false"))
	if err != nil {
		errs = append(errs, err)
	}

	if len(errs) != 0 {
		return nil, errs
	}

	// Load PrimaryDB connections
	sqlClient, err := sql.Open("mysql", DSN)
	if err != nil {
		return nil, []error{errors.New("failed initializing sqlClient"), err}
	}
	err = sqlClient.Ping()
	if err != nil {
		return nil, []error{errors.New("failed ping to sql db"), err}
	}

	// Load Read Replica DB connection
	readSQLClient, err := sql.Open("mysql", readDSN)
	if err != nil {
		return nil, []error{errors.New("failed initializing readSqlClient"), err}
	}
	err = readSQLClient.Ping()
	if err != nil {
		return nil, []error{errors.New("failed to ping read replica sql db"), err}
	}

	// Load Redis connection
	redisClient := redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%s:%s", redisHost, redisPort),
		Password: "",
		DB:       0,
	})
	if err := redisClient.Ping(context.Background()).Err(); err != nil {
		return nil, []error{errors.New("failed ping to redis db"), err}
	}

	var logger *zap.Logger
	if !DEBUG {
		logger, err = zap.NewProduction()
		if err != nil {
			panic("Failed init logger")
		}
	}
	if DEBUG {
		logger, err = zap.NewDevelopment()
		if err != nil {
			panic("Failed init logger")
		}
	}
	log := logger.Sugar()

	return &Core{
		Debug: DEBUG,
		Log:   log,
		Env: Environment{
			InstanceUUID:  instanceUUID,
			MetricsAPIKey: metricsAPIKey,
		},
		RedisClient: redisClient,
		RDB:         readSQLClient,
		WDB:         sqlClient,
	}, nil
}
