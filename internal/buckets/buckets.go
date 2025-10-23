// Package buckets which handles bucketing user usage to charge user
package buckets

import (
	"context"
	"database/sql"
	"fmt"
	"sybil-api/internal/database"
	"sybil-api/internal/metrics"
	"sybil-api/internal/shared"
	"sync"
	"time"

	"go.uber.org/zap"
)

type UsageCache struct {
	buckets       map[uint64]*bucket
	killedBuckets map[uint64]*bucket
	mu            sync.Mutex
	log           *zap.SugaredLogger
	db            *sql.DB
}

type bucket struct {
	mu           sync.Mutex
	userID       uint64
	totalCredits uint64
	qim          map[string]*shared.ProcessedQueryInfo
	inflight     uint64
	timer        *time.Timer
}

func NewUsageCache(log *zap.SugaredLogger, db *sql.DB) *UsageCache {
	return &UsageCache{
		db:            db,
		log:           log,
		buckets:       map[uint64]*bucket{},
		killedBuckets: map[uint64]*bucket{},
	}
}

func (c *UsageCache) Shutdown() {
	c.log.Info("Shutting down cache")
	wg := sync.WaitGroup{}
	for {
		c.mu.Lock()
		total := uint64(0)
		for _, b := range c.buckets {
			if b.timer != nil {
				b.timer.Stop()
			}
			total += b.inflight
		}
		c.mu.Unlock()
		if total == 0 {
			break
		}
		time.Sleep(1 * time.Second)
	}
	for _, b := range c.buckets {
		wg.Add(1)
		go func() {
			c.Flush(b.userID)
			wg.Done()
		}()
	}
	wg.Wait()
}

func (c *UsageCache) AddInFlightToBucket(userID uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	b := c.GetBucket(userID)
	b.addInflight()
}

func (c *UsageCache) RemoveInFlightFromBucket(userID uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	b := c.GetBucket(userID)
	b.decInflight()
}

func (b *bucket) addInflight() {
	b.mu.Lock()
	b.inflight++
	b.mu.Unlock()
}

func (b *bucket) decInflight() {
	b.mu.Lock()
	b.inflight--
	b.mu.Unlock()
}

func (b *bucket) AddRequest(c *UsageCache, pqi *shared.ProcessedQueryInfo, requestID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.qim[requestID] = pqi

	// Case inflight requests and fresh bucket, set timer
	if b.totalCredits == 0 && b.timer == nil {
		c.log.Info("Registering flush for bucket")
		b.timer = time.AfterFunc(shared.BucketFlushInterval, func() {
			retry := c.Flush(b.userID)
			for retry != 0 {
				c.log.Warn("Flush requested retry, waiting...")
				time.Sleep(retry)
				retry = c.Flush(b.userID)
			}
		})
	}
	b.totalCredits += pqi.TotalCredits
	modelLabel := fmt.Sprintf("%d-%s", pqi.ModelID, pqi.Model)
	metrics.InflightRequests.WithLabelValues(fmt.Sprintf("%d", pqi.UserID)).Set(float64(b.inflight))
	metrics.RequestDuration.WithLabelValues(modelLabel, pqi.Endpoint).Observe(pqi.TotalTime.Seconds())
	if pqi.TimeToFirstToken != time.Duration(0) {
		metrics.TimeToFirstToken.WithLabelValues(modelLabel, pqi.Endpoint).Observe(pqi.TimeToFirstToken.Seconds())
	}
	creditsUsed := shared.CalculateCredits(pqi.Usage, pqi.Cost.InputCredits, pqi.Cost.OutputCredits, pqi.Cost.CanceledCredits)
	metrics.CreditUsage.WithLabelValues(modelLabel, pqi.Endpoint, "total").Add(float64(creditsUsed))
	metrics.RequestCount.WithLabelValues(modelLabel, pqi.Endpoint, "success").Inc()
	if pqi.Usage != nil {
		metrics.TokensPerSecond.WithLabelValues(modelLabel, pqi.Endpoint).Observe(float64(pqi.Usage.CompletionTokens) / pqi.TotalTime.Seconds())
		metrics.PromptTokens.WithLabelValues(modelLabel, pqi.Endpoint).Add(float64(pqi.Usage.PromptTokens))
		metrics.CompletionTokens.WithLabelValues(modelLabel, pqi.Endpoint).Add(float64(pqi.Usage.CompletionTokens))
		metrics.TotalTokens.WithLabelValues(modelLabel, pqi.Endpoint).Add(float64(pqi.Usage.TotalTokens))
		if pqi.Usage.IsCanceled {
			metrics.CanceledRequests.WithLabelValues(modelLabel, fmt.Sprintf("%d", pqi.UserID)).Inc()
		}
	}

	// Case no inflight requests so we should flush right away
	if b.inflight >= 1 && b.timer != nil {
		return
	}

	c.log.Info("Executing flush from no more inflights")
	if b.timer != nil {
		// This is the case where inflight goes to 0 and flush has already been
		// called Need to make sure we are doing this correctly w/ mutexes, this
		// may not even be possible?
		ok := b.timer.Stop()
		if !ok {
			c.log.Info("Flush is already executed")
			return
		}
	}

	go func() {
		retry := c.Flush(b.userID)
		for retry != 0 {
			c.log.Warn("Flush requested retry, waiting...")
			time.Sleep(retry)
			retry = c.Flush(b.userID)
		}
	}()
}

func (c *UsageCache) GetBucket(userID uint64) *bucket {
	b, ok := c.buckets[userID]
	if !ok {
		b = &bucket{qim: map[string]*shared.ProcessedQueryInfo{}, userID: userID}
		c.buckets[userID] = b
	}
	return b
}

func (c *UsageCache) AddRequestToBucket(userID uint64, pqi *shared.ProcessedQueryInfo, id string) {
	if pqi.TotalCredits == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	bucket := c.GetBucket(userID)
	bucket.AddRequest(c, pqi, id)
}

func (c *UsageCache) Flush(userID uint64) time.Duration {
	c.log.Info("Starting flush")
	c.mu.Lock()
	b, ok := c.buckets[userID]
	if !ok {
		c.mu.Unlock()
		return 0
	}

	_, ok = c.killedBuckets[userID]
	if ok {
		c.mu.Unlock()
		return shared.BucketRetryDelay
	}
	c.killedBuckets[userID] = b
	delete(c.buckets, userID)
	if b.inflight != 0 {
		c.buckets[userID] = &bucket{
			userID:   userID,
			inflight: b.inflight,
			qim:      map[string]*shared.ProcessedQueryInfo{},
		}
	}
	c.mu.Unlock()

	defer func() {
		// This will also trigger on fail, will need to revisit adding retries
		c.mu.Lock()
		delete(c.killedBuckets, userID)
		c.mu.Unlock()
	}()

	requestsUsed := uint(len(b.qim))

	success := false
	var err error
	for range shared.MaxFlushRetries {
		ctx := context.Background()
		err = database.ExecuteTransaction(ctx, c.db, []func(*sql.Tx) error{
			func(tx *sql.Tx) error {
				return database.ChargeUser(ctx, tx, userID, requestsUsed, b.totalCredits)
			},
		})
		if err != nil {
			c.log.Errorw("Failed to execute transaction", "error", err)
			time.Sleep(5 * time.Second)
			continue
		}
		err = database.SaveRequests(c.db, b.qim, c.log)
		if err != nil {
			c.log.Errorw("Failed to insert records", "error", err)
			break
		}

		success = true
		break
	}
	if !success {
		c.log.Errorw("Failed 3 times with error", "error", err)
		metrics.ErrorCount.WithLabelValues("unknown", "unknown", fmt.Sprintf("%d", b.userID), "save_requests").Inc()
		return 0
	}
	c.log.Infow("Flushed bucket", "user_id", userID, "total_credits_used", b.totalCredits, "requests", len(b.qim))
	return 0
}
