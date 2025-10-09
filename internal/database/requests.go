// Package database defines the insertions and transactions to the database
package database

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sybil-api/internal/shared"
	"time"

	"go.uber.org/zap"
)

type DailyStats struct {
	Date                 string
	UserID               uint64
	Model                string
	ModelID              uint64
	RequestCount         uint64
	InputTokens          uint64
	OutputTokens         uint64
	TotalSpend           uint64
	TimeToFirstToken     int64
	TotalTime            int64
	CanceledRequestCount uint64
}

// SaveRequests saves the request details and updates user credits
func SaveRequests(db *sql.DB, qim map[string]*shared.ProcessedQueryInfo, log *zap.SugaredLogger) error {
	requestSQLStr := `INSERT INTO request (
            user_id, request_id, endpoint,
            prompt_tokens, completion_tokens,
            time_to_first_token, total_time, created_at, model_id
        ) VALUES`

	statsSQLStr := `INSERT INTO daily_stats (
		date, user_id, model, request_count, input_tokens, output_tokens, total_spend, time_to_first_token, total_time, canceled_requests, model_id
	) VALUES`

	today := time.Now().Format("2006-01-02")

	aggregated := make(map[string]*DailyStats)

	requestVals := []any{}
	statsVals := []any{}

	if len(qim) == 0 {
		return nil
	}

	for id, qi := range qim {
		key := fmt.Sprintf("%d", qi.ModelID)
		if _, ok := aggregated[key]; !ok {
			aggregated[key] = &DailyStats{
				UserID:  qi.UserID,
				Model:   qi.Model,
				ModelID: qi.ModelID,
			}
		}
		existing := aggregated[key]
		existing.RequestCount += 1
		existing.InputTokens += qi.Usage.PromptTokens
		existing.OutputTokens += qi.Usage.CompletionTokens
		existing.TotalSpend += qi.TotalCredits
		if !qi.Usage.IsCanceled {
			existing.TimeToFirstToken += qi.TimeToFirstToken.Milliseconds()
			existing.TotalTime += qi.TotalTime.Milliseconds()
		}
		if qi.Usage.IsCanceled {
			existing.CanceledRequestCount += 1
			continue
		}
		requestSQLStr += "(?, ?, ?, ?, ?, ?, ?, ?, ?),"
		requestVals = append(requestVals,
			qi.UserID, id, qi.Endpoint,
			qi.Usage.PromptTokens, qi.Usage.CompletionTokens,
			qi.TimeToFirstToken.Milliseconds(), qi.TotalTime.Milliseconds(),
			qi.CreatedAt,
			qi.ModelID,
		)
	}

	for _, val := range aggregated {
		statsSQLStr += "(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?),"
		statsVals = append(statsVals, today, val.UserID, val.Model, val.RequestCount, val.InputTokens, val.OutputTokens, val.TotalSpend, val.TimeToFirstToken, val.TotalTime, val.CanceledRequestCount, val.ModelID)
	}

	requestSQLStr = strings.TrimSuffix(requestSQLStr, ",")
	statsSQLStr = strings.TrimSuffix(statsSQLStr, ",")
	statsSQLStr += ` ON DUPLICATE KEY UPDATE
		canceled_requests = canceled_requests + VALUES(canceled_requests),
		request_count = request_count + VALUES(request_count),
		input_tokens = input_tokens + VALUES(input_tokens),
		output_tokens = output_tokens + VALUES(output_tokens),
		total_spend = total_spend + VALUES(total_spend),
		time_to_first_token = time_to_first_token + VALUES(time_to_first_token),
		total_time = total_time + VALUES(total_time)`

	// Save request history
	if len(requestVals) > 0 {
		_, err := db.Exec(requestSQLStr, requestVals...)
		if err != nil {
			return fmt.Errorf("failed to save request: %w", err)
		}
	}

	_, err := db.Exec(statsSQLStr, statsVals...)
	if err != nil {
		return fmt.Errorf("failed to save request: %w", err)
	}

	return nil
}

func UpdateUserCredits(ctx context.Context, tx *sql.Tx, userID uint64, creditsUsed uint64) error {
	// First try to use as many plan credits as possible
	var credits uint64
	var endCredits uint64

	// Get current plan credits for the user
	err := tx.QueryRowContext(ctx, "SELECT credits FROM user WHERE id = ? FOR UPDATE", userID).Scan(&credits)
	if err != nil {
		return fmt.Errorf("failed to get current plan credits: %w", err)
	}

	// Cant use max because of uint overflow
	if creditsUsed > credits {
		endCredits = 0
	} else {
		endCredits = credits - creditsUsed
	}
	// Update user's plan credits
	_, err = tx.ExecContext(ctx, `
    UPDATE user 
    SET credits = ?
    WHERE id = ?`,
		endCredits, userID,
	)
	if err != nil {
		return fmt.Errorf("failed to update credits: %w", err)
	}

	return err
}

// ExecuteTransaction executes one transaction with one or multiple database executions.
func ExecuteTransaction(ctx context.Context, writeDB *sql.DB, fns []func(*sql.Tx) error) error {
	tx, err := writeDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	// Execute all functions in the transaction
	for _, fn := range fns {
		if err := fn(tx); err != nil {
			return fmt.Errorf("failed to execute transaction function: %w", err)
		}
	}

	// Commit the transaction if all functions succeeded
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}
