package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/LeXwDeX/Plinth/internal/administration"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

func (r *Repository) QueryAdministrationUsage(
	ctx context.Context,
	query administration.UsageQuery,
) ([]administration.UsageBucket, error) {
	source, err := r.administrationGenerationSource(ctx)
	if err != nil {
		return nil, err
	}
	clauses := []string{"created_unix >= ?"}
	args := []any{unixSeconds(query.Since)}
	if strings.TrimSpace(query.UserID) != "" {
		clauses = append(clauses, "user_id = ?")
		args = append(args, strings.TrimSpace(query.UserID))
	}
	if query.Until != nil {
		clauses = append(clauses, "created_unix <= ?")
		args = append(args, unixSeconds(*query.Until))
	}
	rows, err := r.db.QueryContext(
		ctx,
		`SELECT
			user_id,
			user_name,
			user_email,
			task_type,
			task_status,
			1,
 estimated_credits,
 created_unix, native_quote_json, params_json, estimate_offering_json, charged
		FROM (`+source+`) AS generation
		WHERE `+strings.Join(clauses, " AND ")+`
		ORDER BY user_id, task_type, task_status`,
		args...,
	)
	if err != nil {
		return nil, fmt.Errorf("查询管理用量统计: %w", err)
	}
	defer rows.Close() // All malformed native-quote exits must release the reader.
	buckets := make([]administration.UsageBucket, 0)
	legacyGroups := map[string]int{}
	for rows.Next() {
		var bucket administration.UsageBucket
		var lastActiveUnix float64
		var quote sql.NullString
		var params string
		var estimateOffering sql.NullString
		var charged bool
		if err := rows.Scan(
			&bucket.UserID,
			&bucket.UserName,
			&bucket.UserEmail,
			&bucket.TaskType,
			&bucket.TaskStatus,
			&bucket.TaskCount,
			&bucket.EstimatedCredits,
			&lastActiveUnix, &quote, &params, &estimateOffering, &charged,
		); err != nil {
			rows.Close()
			return nil, fmt.Errorf("读取管理用量统计: %w", err)
		}
		if quote.Valid {
			if e := json.Unmarshal([]byte(quote.String), &bucket.NativeQuote); e != nil {
				return nil, e
			}
			if e := bucket.NativeQuote.Validate(); e != nil {
				return nil, e
			}
			bucket.EstimatedCredits = 0
		} else {
			bucket.EstimatedCredits = frozenTaskEstimate(bucket.EstimatedCredits, estimateOffering, params)
		}
		if !charged {
			// Counted as a task, not as spent credits (#327): the same rule as the
			// session total.
			bucket.Uncharged, bucket.NativeQuote, bucket.EstimatedCredits = true, nil, 0
		}
		bucket.LastActiveAt = usageUnixTime(lastActiveUnix)
		if bucket.NativeQuote == nil {
			keyBytes, _ := json.Marshal([]any{bucket.UserID, bucket.UserName, bucket.UserEmail, bucket.TaskType, bucket.TaskStatus, bucket.Uncharged})
			key := string(keyBytes)
			if at, ok := legacyGroups[key]; ok {
				old := &buckets[at]
				a := schema.PriceAccumulator{Legacy: old.EstimatedCredits}
				if e := a.Add(bucket.EstimatedCredits, nil); e != nil {
					return nil, e
				}
				old.EstimatedCredits = a.Legacy
				old.TaskCount += bucket.TaskCount
				if bucket.LastActiveAt.After(old.LastActiveAt) {
					old.LastActiveAt = bucket.LastActiveAt
				}
				continue
			}
			legacyGroups[key] = len(buckets)
		}
		buckets = append(buckets, bucket)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("遍历管理用量统计: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("关闭管理用量统计结果集: %w", err)
	}
	return buckets, nil
}

func usageUnixTime(value float64) time.Time {
	seconds, fraction := math.Modf(value)
	nanoseconds := int64(math.Round(fraction * float64(time.Second)))
	if nanoseconds == int64(time.Second) {
		seconds++
		nanoseconds = 0
	}
	return time.Unix(int64(seconds), nanoseconds).UTC()
}
