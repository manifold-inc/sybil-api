// Package inference includes all routes and functionality for Sybil Inference
package inference

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"net/http"
	"time"

	"sybil-api/internal/buckets"
	"sybil-api/internal/shared"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

type InferenceManager struct {
	WDB         *sql.DB
	RDB         *sql.DB
	RedisClient *redis.Client
	Log         *zap.SugaredLogger
	Debug       bool
	HTTPClient  *http.Client
	usageCache  *buckets.UsageCache
}

func NewInferenceManager(wdb *sql.DB, rdb *sql.DB, redisClient *redis.Client, log *zap.SugaredLogger, debug bool) (*InferenceManager, error) {
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

	tr := &http.Transport{
		Dial: (&net.Dialer{
			Timeout: 2 * time.Second,
		}).Dial,
		TLSHandshakeTimeout: 2 * time.Second,
		DisableKeepAlives:   false,
	}
	httpClient := &http.Client{
		Transport: tr,
		Timeout:   shared.DefaultHTTPTimeout,
	}

	usageCache := buckets.NewUsageCache(log, wdb)

	return &InferenceManager{
		WDB:         wdb,
		RDB:         rdb,
		RedisClient: redisClient,
		Log:         log,
		Debug:       debug,
		HTTPClient:  httpClient,
		usageCache:  usageCache,
	}, nil
}

func (im *InferenceManager) ShutDown() {
	if im.usageCache != nil {
		im.usageCache.Shutdown()
	}
}
