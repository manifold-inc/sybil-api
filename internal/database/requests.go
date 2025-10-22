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

type RequestTransaction struct {
	ID        string
	CreatedAt time.Time
	Credits   uint64
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

func ChargeUser(ctx context.Context, tx *sql.Tx, userID uint64, transactions []RequestTransaction) error {
	var planRequests uint
	var credits uint64
	err := tx.QueryRowContext(ctx, "SELECT COALESCE(plan_requests, 0), credits FROM user WHERE id = ? FOR UPDATE", userID).Scan(&planRequests, &credits)
	if err != nil {
		return fmt.Errorf("failed to get user plan data: %w", err)
	}
	requestsUsed := uint(len(transactions))

	switch {
	case planRequests >= requestsUsed:
		_, err = tx.ExecContext(ctx, "UPDATE user SET plan_requests = plan_requests - ? WHERE id = ?", requestsUsed, userID)
		if err != nil {
			return fmt.Errorf("failed to update user plan requests: %w", err)
		}
		return nil
	default:
		// uses all plan_requests and charges for remaining requests via credits
		creditsCharged := uint64(0)
		for i := planRequests; i < requestsUsed; i++ {
			creditsCharged += transactions[i].Credits
		}

		balance := uint64(0)
		if creditsCharged < credits {
			balance = credits - creditsCharged
		} else {
			balance = 0
		}

		_, err = tx.ExecContext(ctx, "UPDATE user SET plan_requests = 0, credits = ? WHERE id = ?", balance, userID)
		if err != nil {
			return fmt.Errorf("failed to update user plan requests and credits: %w", err)
		}
		return nil
	}
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
