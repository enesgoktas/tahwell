package outbox

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

type mockDispatcher struct {
	mu           sync.Mutex
	attempts     int
	failUntil    int
	lastEndpoint string
	lastPayload  any
}

func (m *mockDispatcher) DispatchGeneric(ctx context.Context, endpointURL, token string, payload any) (map[string]any, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.attempts++
	m.lastEndpoint = endpointURL
	m.lastPayload = payload

	if m.attempts <= m.failUntil {
		return nil, fmt.Errorf("simulated 503 Service Unavailable (attempt %d)", m.attempts)
	}
	return map[string]any{"trip_id": "RECOVERED-TRIP-123", "status": "ok"}, nil
}

func TestWorker_RetryRecovery(t *testing.T) {
	db, store := setupTestDB(t)
	defer db.Close()

	ctx := context.Background()

	// Mock dispatcher that fails on first 2 attempts, succeeds on 3rd
	mock := &mockDispatcher{failUntil: 2}

	cfg := &WorkerConfig{
		PollInterval: 10 * time.Millisecond,
		BatchSize:    5,
		BaseBackoff:  10 * time.Millisecond,
		MaxBackoff:   50 * time.Millisecond,
	}
	worker := NewWorker(store, nil, mock, cfg)

	// Enqueue task ready now
	task := &Task{
		TaskID:      "task-retry-1",
		ProfileID:   "prof-1",
		ReferenceID: "ORD-999",
		TaskType:    TypeOrderDispatch,
		TargetURL:   "https://api.carrier.example/shipments",
		Payload:     `{"order_id":"ORD-999"}`,
		MaxRetries:  5,
		NextRetryAt: time.Now().UTC().Add(-1 * time.Second),
	}
	if err := store.Enqueue(ctx, task); err != nil {
		t.Fatalf("failed enqueuing: %v", err)
	}

	// Attempt 1: should fail
	worker.ProcessBatch(ctx)
	t1, _ := store.Get(ctx, "task-retry-1")
	if t1.RetryCount != 1 || t1.Status != StatusPending {
		t.Errorf("after attempt 1: retryCount=%d, status=%s", t1.RetryCount, t1.Status)
	}

	// Advance time for attempt 2
	_ = store.ScheduleRetry(ctx, "task-retry-1", time.Now().UTC().Add(-1*time.Second))
	worker.ProcessBatch(ctx)
	t2, _ := store.Get(ctx, "task-retry-1")
	if t2.RetryCount != 2 || t2.Status != StatusPending {
		t.Errorf("after attempt 2: retryCount=%d, status=%s", t2.RetryCount, t2.Status)
	}

	// Advance time for attempt 3: should succeed
	_ = store.ScheduleRetry(ctx, "task-retry-1", time.Now().UTC().Add(-1*time.Second))
	worker.ProcessBatch(ctx)
	t3, _ := store.Get(ctx, "task-retry-1")
	if t3.Status != StatusCompleted {
		t.Errorf("after attempt 3: expected COMPLETED, got %s", t3.Status)
	}
}

func TestWorker_DeadLetterOnMaxRetries(t *testing.T) {
	db, store := setupTestDB(t)
	defer db.Close()

	ctx := context.Background()

	// Dispatcher that always fails
	mock := &mockDispatcher{failUntil: 999}

	cfg := &WorkerConfig{
		PollInterval: 10 * time.Millisecond,
		BatchSize:    5,
		BaseBackoff:  5 * time.Millisecond,
		MaxBackoff:   20 * time.Millisecond,
	}
	worker := NewWorker(store, nil, mock, cfg)

	task := &Task{
		TaskID:      "task-dl-1",
		ReferenceID: "ORD-DL",
		TargetURL:   "http://dead.url",
		Payload:     `{}`,
		MaxRetries:  2,
		NextRetryAt: time.Now().UTC().Add(-1 * time.Second),
	}
	_ = store.Enqueue(ctx, task)

	// Attempt 1: fails, retryCount=1
	worker.ProcessBatch(ctx)
	// Force ready for attempt 2
	_ = store.ScheduleRetry(ctx, "task-dl-1", time.Now().UTC().Add(-1*time.Second))

	// Attempt 2: hits maxRetries (2) -> DEAD_LETTER
	worker.ProcessBatch(ctx)

	finalTask, _ := store.Get(ctx, "task-dl-1")
	if finalTask.Status != StatusDeadLetter {
		t.Errorf("expected DEAD_LETTER, got %s", finalTask.Status)
	}
}
