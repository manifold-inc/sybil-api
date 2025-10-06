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

type TargonManager struct {
	Log            *zap.SugaredLogger
	TargonAPIKey   string
	TargonEndpoint string
	WDB            *sql.DB
	RedisClient    *redis.Client
	HTTPClient     *http.Client
}

func NewTargonManager(sqlClient *sql.DB, redisClient *redis.Client, log *zap.SugaredLogger) (*TargonManager, error) {

	err := sqlClient.Ping()
	if err != nil {
		return nil, errors.New("failed to ping sql client")
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

	return &TargonManager{
		Log:            log,
		TargonAPIKey:   targonAPIKey,
		TargonEndpoint: targonEndpoint,
		WDB:            sqlClient,
		RedisClient:    redisClient,
		HTTPClient:     &httpClient,
	}, nil
}
