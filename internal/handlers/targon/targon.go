// Package targon includes all routes and functionality for Targon functionality
package targon

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"net/http"
	"sybil-api/internal/shared"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

type TargonHandler struct {
	Log            *zap.SugaredLogger
	TargonAPIKey   string
	TargonEndpoint string
	WDB            *sql.DB
	RDB            *sql.DB
	RedisClient    *redis.Client
	HTTPClient     *http.Client
}

func NewTargonHandler(wdb *sql.DB, rdb *sql.DB, redisClient *redis.Client, log *zap.SugaredLogger) (*TargonHandler, error) {

	err := wdb.Ping()
	if err != nil {
		return nil, errors.New("failed to ping write db")
	}

	err = rdb.Ping()
	if err != nil {
		return nil, errors.New("failed to ping read replica db")
	}

	err = redisClient.Ping(context.Background()).Err()
	if err != nil {
		return nil, errors.New("failed to ping redis client")
	}

	targonAPIKey, err := shared.SafeEnv("TARGON_API_KEY")
	if err != nil {
		return nil, errors.New("failed to get targon api key")
	}
	targonEndpoint, err := shared.SafeEnv("TARGON_ENDPOINT")
	if err != nil {
		return nil, errors.New("failed to get targon endpoint")
	}

	tr := &http.Transport{
		Dial: (&net.Dialer{
			Timeout: 2 * time.Second,
		}).Dial,
		TLSHandshakeTimeout: 2 * time.Second,
		DisableKeepAlives:   false,
	}
	httpClient := http.Client{Transport: tr, Timeout: 2 * time.Minute}

	return &TargonHandler{
		Log:            log,
		TargonAPIKey:   targonAPIKey,
		TargonEndpoint: targonEndpoint,
		WDB:            wdb,
		RDB:            rdb,
		RedisClient:    redisClient,
		HTTPClient:     &httpClient,
	}, nil
}
