package outbox

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func setupTestDB(t *testing.T) (*sql.DB, *Store) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_outbox.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("failed opening sqlite: %v", err)
	}
	store, err := NewStore(db)
	if err != nil {
		t.Fatalf("failed creating outbox store: %v", err)
	}
	return db, store
}

func TestStore_EnqueueAndFetchDue(t *testing.T) {
	db, store := setupTestDB(t)
	defer db.Close()

	ctx := context.Background()
	now := time.Now().UTC()

	// Task 1: Due now
	t1 := &Task{
		TaskID:      "task-1",
		ProfileID:   "profile-a",
		ReferenceID: "ORD-001",
		TaskType:    TypeOrderDispatch,
		TargetURL:   "https://api.carrier.com/orders",
		Payload:     `{"order":"001"}`,
		NextRetryAt: now.Add(-1 * time.Minute), // in past, ready
	}

	// Task 2: Scheduled in future (not due)
	t2 := &Task{
		TaskID:      "task-2",
		ProfileID:   "profile-a",
		ReferenceID: "ORD-002",
		TaskType:    TypeOrderDispatch,
		TargetURL:   "https://api.carrier.com/orders",
		Payload:     `{"order":"002"}`,
		NextRetryAt: now.Add(10 * time.Minute), // future, not ready
	}

	if err := store.Enqueue(ctx, t1); err != nil {
		t.Fatalf("failed to enqueue t1: %v", err)
	}
	if err := store.Enqueue(ctx, t2); err != nil {
		t.Fatalf("failed to enqueue t2: %v", err)
	}

	due, err := store.FetchDue(ctx, 10)
	if err != nil {
		t.Fatalf("failed to fetch due tasks: %v", err)
	}

	if len(due) != 1 {
		t.Fatalf("expected 1 due task, got %d", len(due))
	}
	if due[0].TaskID != "task-1" {
		t.Errorf("expected task-1, got %s", due[0].TaskID)
	}
}

func TestStore_LifecycleTransitions(t *testing.T) {
	db, store := setupTestDB(t)
	defer db.Close()

	ctx := context.Background()

	task := &Task{
		TaskID:      "task-100",
		ProfileID:   "profile-b",
		ReferenceID: "ORD-100",
		TaskType:    TypeERPCallback,
		TargetURL:   "https://erp.shipper.com/callback",
		Payload:     `{"status":"DELIVERED"}`,
	}

	if err := store.Enqueue(ctx, task); err != nil {
		t.Fatalf("failed enqueue: %v", err)
	}

	// 1. Mark Failed with retry
	nextRetry := time.Now().UTC().Add(30 * time.Second)
	if err := store.MarkFailed(ctx, "task-100", "503 Service Unavailable", nextRetry); err != nil {
		t.Fatalf("failed MarkFailed: %v", err)
	}

	fetched, err := store.Get(ctx, "task-100")
	if err != nil {
		t.Fatalf("failed Get: %v", err)
	}
	if fetched.RetryCount != 1 {
		t.Errorf("expected retry count 1, got %d", fetched.RetryCount)
	}
	if fetched.LastError != "503 Service Unavailable" {
		t.Errorf("expected last error 503, got %s", fetched.LastError)
	}

	// 2. Mark Dead Letter
	if err := store.MarkDeadLetter(ctx, "task-100", "Max retries exceeded"); err != nil {
		t.Fatalf("failed MarkDeadLetter: %v", err)
	}

	deadTask, err := store.Get(ctx, "task-100")
	if err != nil {
		t.Fatalf("failed Get: %v", err)
	}
	if deadTask.Status != StatusDeadLetter {
		t.Errorf("expected status DEAD_LETTER, got %s", deadTask.Status)
	}

	// 3. Mark Completed
	if err := store.MarkCompleted(ctx, "task-100"); err != nil {
		t.Fatalf("failed MarkCompleted: %v", err)
	}

	doneTask, err := store.Get(ctx, "task-100")
	if err != nil {
		t.Fatalf("failed Get: %v", err)
	}
	if doneTask.Status != StatusCompleted {
		t.Errorf("expected status COMPLETED, got %s", doneTask.Status)
	}
}

func TestStore_StatsAndList(t *testing.T) {
	db, store := setupTestDB(t)
	defer db.Close()

	ctx := context.Background()

	_ = store.Enqueue(ctx, &Task{TaskID: "t-1", ReferenceID: "R1", TargetURL: "http://x", Payload: "{}"})
	_ = store.Enqueue(ctx, &Task{TaskID: "t-2", ReferenceID: "R2", TargetURL: "http://x", Payload: "{}"})
	_ = store.MarkCompleted(ctx, "t-1")

	stats, err := store.Stats(ctx)
	if err != nil {
		t.Fatalf("failed getting stats: %v", err)
	}

	if stats[string(StatusCompleted)] != 1 {
		t.Errorf("expected 1 completed, got %d", stats[string(StatusCompleted)])
	}
	if stats[string(StatusPending)] != 1 {
		t.Errorf("expected 1 pending, got %d", stats[string(StatusPending)])
	}

	list, err := store.List(ctx, string(StatusPending), 10)
	if err != nil || len(list) != 1 {
		t.Fatalf("expected 1 pending task in list, got %d (err: %v)", len(list), err)
	}
}
