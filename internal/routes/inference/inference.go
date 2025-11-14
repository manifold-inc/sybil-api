// Package inference includes all routes and functionality for Sybil Inference
package inference

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"sybil-api/internal/buckets"
	"sybil-api/internal/shared"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

type InferenceManager struct {
	WDB          *sql.DB
	RDB          *sql.DB
	RedisClient  *redis.Client
	Log          *zap.SugaredLogger
	Debug        bool
	httpClients  map[string]*http.Client
	clientsMutex sync.RWMutex
	usageCache   *buckets.UsageCache
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

	usageCache := buckets.NewUsageCache(log, wdb)

	return &InferenceManager{
		WDB:         wdb,
		RDB:         rdb,
		RedisClient: redisClient,
		Log:         log,
		Debug:       debug,
		httpClients: make(map[string]*http.Client),
		usageCache:  usageCache,
	}, nil
}

func (im *InferenceManager) getHTTPClient(modelURL string) *http.Client {
	parsedURL, err := url.Parse(modelURL)
	if err != nil {
		im.Log.Warnw("Failed to parse model URL, using full URL as key", "url", modelURL, "error", err)
		parsedURL = &url.URL{Host: modelURL}
	}
	host := parsedURL.Host

	im.clientsMutex.RLock()
	if client, exists := im.httpClients[host]; exists {
		im.clientsMutex.RUnlock()
		return client
	}
	im.clientsMutex.RUnlock()

	im.clientsMutex.Lock()
	defer im.clientsMutex.Unlock()

	if client, exists := im.httpClients[host]; exists {
		return client
	}

	tr := &http.Transport{
		Dial: (&net.Dialer{
			Timeout: 2 * time.Second,
		}).Dial,
		TLSHandshakeTimeout: 2 * time.Second,
		DisableKeepAlives:   false,
	}
	client := &http.Client{Transport: tr, Timeout: 10 * time.Minute}

	im.httpClients[host] = client
	im.Log.Infow("Created new HTTP client for host", "host", host, "full_url", modelURL)

	return client
}


func (im *InferenceManager) Process(inv *Invocation, resp Responder) (*shared.RequestInfo, *shared.ResponseInfo, error) {
	reqInfo, preprocessError := im.Preprocess(inv)
	if preprocessError != nil {
		return nil, nil, resp.SendError(preprocessError)
	}

	im.usageCache.AddInFlightToBucket(reqInfo.UserID)
	defer im.usageCache.RemoveInFlightFromBucket(reqInfo.UserID)

	resInfo, queryError := im.QueryModels(inv, reqInfo, resp)
	if queryError != nil {
		return reqInfo, nil, resp.SendError(queryError)
	}

	return reqInfo, resInfo, nil
}

func (im *InferenceManager) ShutDown() {
	if im.usageCache != nil {
		im.usageCache.Shutdown()
	}
}