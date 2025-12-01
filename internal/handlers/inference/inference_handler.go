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

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

type InferenceHandler struct {
	WDB          *sql.DB
	RDB          *sql.DB
	RedisClient  *redis.Client
	Log          *zap.SugaredLogger
	Debug        bool
	httpClients  map[string]*http.Client
	clientsMutex sync.RWMutex
	usageCache   *buckets.UsageCache
}

func NewInferenceHandler(wdb *sql.DB, rdb *sql.DB, redisClient *redis.Client, log *zap.SugaredLogger, debug bool) (*InferenceHandler, error) {
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

	return &InferenceHandler{
		WDB:         wdb,
		RDB:         rdb,
		RedisClient: redisClient,
		Log:         log,
		Debug:       debug,
		httpClients: make(map[string]*http.Client),
		usageCache:  usageCache,
	}, nil
}

func (im *InferenceHandler) getHTTPClient(modelURL string) *http.Client {
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

func (im *InferenceHandler) ShutDown() {
	if im.usageCache != nil {
		im.usageCache.Shutdown()
	}
}

func logWithFields(logger *zap.SugaredLogger, fields map[string]string) *zap.SugaredLogger {
	if len(fields) == 0 {
		return logger
	}
	args := make([]interface{}, 0, len(fields)*2)
	for k, v := range fields {
		args = append(args, k, v)
	}
	return logger.With(args...)
}
