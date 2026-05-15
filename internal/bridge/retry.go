package bridge

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/jelmersnoeck/forge/internal/discord"
)

// RetryItem is an outbound Discord message that failed and needs retrying.
type RetryItem struct {
	ThreadID string
	Content  string
	Embed    interface{} // *discordgo.MessageEmbed or nil
	Pin      bool

	retryCount int
	nextRetry  time.Time
}

// retryBackoffs are the backoff durations for each retry attempt.
var retryBackoffs = []time.Duration{
	1 * time.Second,
	4 * time.Second,
	16 * time.Second,
	60 * time.Second,
	5 * time.Minute,
	30 * time.Minute,
}

const deadLetterAge = 6 * time.Hour

// RetryQueue is an in-memory FIFO for failed outbound Discord posts.
type RetryQueue struct {
	mu     sync.Mutex
	items  []RetryItem
	logger *slog.Logger
}

// NewRetryQueue creates an empty retry queue.
func NewRetryQueue(logger *slog.Logger) *RetryQueue {
	return &RetryQueue{logger: logger}
}

// Enqueue adds a failed item to the retry queue.
func (q *RetryQueue) Enqueue(item RetryItem) {
	q.mu.Lock()
	defer q.mu.Unlock()

	backoff := deadLetterAge
	if item.retryCount < len(retryBackoffs) {
		backoff = retryBackoffs[item.retryCount]
	}
	item.nextRetry = time.Now().Add(backoff)
	item.retryCount++
	q.items = append(q.items, item)
}

// Len returns the number of items in the queue.
func (q *RetryQueue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.items)
}

// Drain processes the retry queue until ctx is cancelled.
// On cancellation it gets a 5s grace period to flush remaining items.
func (q *RetryQueue) Drain(ctx context.Context, dc discord.Client) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			// Grace period: 5 seconds to flush
			q.flushWithGrace(dc, 5*time.Second)
			return
		case <-ticker.C:
			q.processReady(ctx, dc)
		}
	}
}

func (q *RetryQueue) processReady(ctx context.Context, dc discord.Client) {
	now := time.Now()
	q.mu.Lock()
	var ready []RetryItem
	var remaining []RetryItem
	for _, item := range q.items {
		if now.After(item.nextRetry) || now.Equal(item.nextRetry) {
			ready = append(ready, item)
		} else {
			remaining = append(remaining, item)
		}
	}
	q.items = remaining
	q.mu.Unlock()

	for _, item := range ready {
		if time.Since(item.nextRetry.Add(-deadLetterAge)) > deadLetterAge {
			q.logger.Warn("dead-lettering retry item",
				"thread", item.ThreadID, "retries", item.retryCount)
			continue
		}

		_, err := dc.PostMessage(ctx, item.ThreadID, item.Content)
		if err != nil {
			q.logger.Warn("retry failed, re-enqueuing",
				"thread", item.ThreadID, "retries", item.retryCount, "error", err)
			q.Enqueue(item)
		}
	}
}

func (q *RetryQueue) flushWithGrace(dc discord.Client, grace time.Duration) {
	ctx, cancel := context.WithTimeout(context.Background(), grace)
	defer cancel()

	q.mu.Lock()
	items := q.items
	q.items = nil
	q.mu.Unlock()

	for _, item := range items {
		select {
		case <-ctx.Done():
			q.logger.Warn("grace period expired, dropping remaining items",
				"remaining", len(items))
			return
		default:
		}
		_, err := dc.PostMessage(ctx, item.ThreadID, item.Content)
		if err != nil {
			q.logger.Warn("grace flush failed", "thread", item.ThreadID, "error", err)
		}
	}
}
