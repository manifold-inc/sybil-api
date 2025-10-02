// Package inference includes all routes and functionality for Sybil Inference
package inference

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

type InferenceManager struct {
	WDB         *sql.DB
	RDB         *sql.DB
	RedisClient *redis.Client
	Log         *zap.SugaredLogger
	Client      *http.Client
	Debug       bool
}

func NewInferenceManager(wdb *sql.DB, rdb *sql.DB, redisClient *redis.Client, log *zap.SugaredLogger, client *http.Client, debug bool) (*InferenceManager, error) {
	// check if the databases are connected
	err := wdb.Ping()
	if err != nil {
		return nil, errors.New("failed ping to sql db")
	}

	err = rdb.Ping()
	if err != nil {
		return nil, errors.New("failed to ping read replica sql db")
	}

	err = redisClient.Ping(context.Background()).Err()
	if err != nil {
		return nil, errors.New("failed ping to redis db")
	}

	return &InferenceManager{
		WDB:         wdb,
		RDB:         rdb,
		RedisClient: redisClient,
		Log:         log,
		Client:      client,
		Debug:       debug,
	}, nil
}
