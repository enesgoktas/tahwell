package outbox

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Store handles persistent transactional outbox operations in SQLite.
type Store struct {
	db *sql.DB
}

// NewStore initializes the outbox schema and returns a new Store.
func NewStore(db *sql.DB) (*Store, error) {
	query := `
	CREATE TABLE IF NOT EXISTS outbox_tasks (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		task_id TEXT NOT NULL UNIQUE,
		profile_id TEXT NOT NULL,
		reference_id TEXT NOT NULL,
		task_type TEXT NOT NULL,
		target_url TEXT NOT NULL,
		auth_token TEXT,
		payload TEXT NOT NULL,
		status TEXT NOT NULL,
		retry_count INTEGER NOT NULL DEFAULT 0,
		max_retries INTEGER NOT NULL DEFAULT 5,
		last_error TEXT,
		next_retry_at DATETIME NOT NULL,
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_outbox_due ON outbox_tasks(status, next_retry_at);
	CREATE INDEX IF NOT EXISTS idx_outbox_ref ON outbox_tasks(reference_id);
	`
	if _, err := db.Exec(query); err != nil {
		return nil, fmt.Errorf("failed initializing outbox table: %w", err)
	}
	return &Store{db: db}, nil
}

// Enqueue inserts a new task into the outbox for background delivery.
func (s *Store) Enqueue(ctx context.Context, t *Task) error {
	now := time.Now().UTC()
	if t.CreatedAt.IsZero() {
		t.CreatedAt = now
	}
	if t.UpdatedAt.IsZero() {
		t.UpdatedAt = now
	}
	if t.NextRetryAt.IsZero() {
		t.NextRetryAt = now
	}
	if t.Status == "" {
		t.Status = StatusPending
	}
	if t.MaxRetries <= 0 {
		t.MaxRetries = 5
	}

	query := `
	INSERT INTO outbox_tasks (
		task_id, profile_id, reference_id, task_type, target_url,
		auth_token, payload, status, retry_count, max_retries,
		last_error, next_retry_at, created_at, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`
	_, err := s.db.ExecContext(ctx, query,
		t.TaskID, t.ProfileID, t.ReferenceID, string(t.TaskType), t.TargetURL,
		t.AuthToken, t.Payload, string(t.Status), t.RetryCount, t.MaxRetries,
		t.LastError, t.NextRetryAt, t.CreatedAt, t.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed enqueuing outbox task: %w", err)
	}
	return nil
}

// FetchDue retrieves tasks that are in PENDING status and ready for retry.
func (s *Store) FetchDue(ctx context.Context, limit int) ([]*Task, error) {
	if limit <= 0 {
		limit = 20
	}
	now := time.Now().UTC()

	query := `
	SELECT id, task_id, profile_id, reference_id, task_type, target_url,
	       auth_token, payload, status, retry_count, max_retries,
	       last_error, next_retry_at, created_at, updated_at
	FROM outbox_tasks
	WHERE status = ? AND next_retry_at <= ?
	ORDER BY next_retry_at ASC
	LIMIT ?
	`
	rows, err := s.db.QueryContext(ctx, query, string(StatusPending), now, limit)
	if err != nil {
		return nil, fmt.Errorf("failed querying due tasks: %w", err)
	}
	defer rows.Close()

	var tasks []*Task
	for rows.Next() {
		var t Task
		var rawType, rawStatus string
		var lastErr sql.NullString
		var authToken sql.NullString

		if err := rows.Scan(
			&t.ID, &t.TaskID, &t.ProfileID, &t.ReferenceID, &rawType, &t.TargetURL,
			&authToken, &t.Payload, &rawStatus, &t.RetryCount, &t.MaxRetries,
			&lastErr, &t.NextRetryAt, &t.CreatedAt, &t.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("failed scanning outbox task: %w", err)
		}

		t.TaskType = TaskType(rawType)
		t.Status = TaskStatus(rawStatus)
		if lastErr.Valid {
			t.LastError = lastErr.String
		}
		if authToken.Valid {
			t.AuthToken = authToken.String
		}

		tasks = append(tasks, &t)
	}

	return tasks, rows.Err()
}

// ScheduleRetry updates the next retry timestamp without incrementing retry_count.
func (s *Store) ScheduleRetry(ctx context.Context, taskID string, nextRetryAt time.Time) error {
	query := `
	UPDATE outbox_tasks
	SET next_retry_at = ?,
	    status = ?,
	    updated_at = ?
	WHERE task_id = ?
	`
	_, err := s.db.ExecContext(ctx, query, nextRetryAt, string(StatusPending), time.Now().UTC(), taskID)
	return err
}

// MarkCompleted marks a task as successfully delivered.
func (s *Store) MarkCompleted(ctx context.Context, taskID string) error {
	query := `
	UPDATE outbox_tasks
	SET status = ?, updated_at = ?
	WHERE task_id = ?
	`
	_, err := s.db.ExecContext(ctx, query, string(StatusCompleted), time.Now().UTC(), taskID)
	return err
}

// MarkFailed increments retry count and schedules next attempt with exponential backoff.
func (s *Store) MarkFailed(ctx context.Context, taskID string, errMsg string, nextRetryAt time.Time) error {
	query := `
	UPDATE outbox_tasks
	SET retry_count = retry_count + 1,
	    last_error = ?,
	    next_retry_at = ?,
	    status = ?,
	    updated_at = ?
	WHERE task_id = ?
	`
	_, err := s.db.ExecContext(ctx, query, errMsg, nextRetryAt, string(StatusPending), time.Now().UTC(), taskID)
	return err
}

// MarkDeadLetter moves an unrecoverable task to dead-letter state after exceeding max retries.
func (s *Store) MarkDeadLetter(ctx context.Context, taskID string, finalErrMsg string) error {
	query := `
	UPDATE outbox_tasks
	SET status = ?,
	    last_error = ?,
	    updated_at = ?
	WHERE task_id = ?
	`
	_, err := s.db.ExecContext(ctx, query, string(StatusDeadLetter), finalErrMsg, time.Now().UTC(), taskID)
	return err
}

// Get retrieves a task by its taskID.
func (s *Store) Get(ctx context.Context, taskID string) (*Task, error) {
	query := `
	SELECT id, task_id, profile_id, reference_id, task_type, target_url,
	       auth_token, payload, status, retry_count, max_retries,
	       last_error, next_retry_at, created_at, updated_at
	FROM outbox_tasks
	WHERE task_id = ?
	`
	row := s.db.QueryRowContext(ctx, query, taskID)

	var t Task
	var rawType, rawStatus string
	var lastErr, authToken sql.NullString

	if err := row.Scan(
		&t.ID, &t.TaskID, &t.ProfileID, &t.ReferenceID, &rawType, &t.TargetURL,
		&authToken, &t.Payload, &rawStatus, &t.RetryCount, &t.MaxRetries,
		&lastErr, &t.NextRetryAt, &t.CreatedAt, &t.UpdatedAt,
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("task %q not found", taskID)
		}
		return nil, err
	}

	t.TaskType = TaskType(rawType)
	t.Status = TaskStatus(rawStatus)
	if lastErr.Valid {
		t.LastError = lastErr.String
	}
	if authToken.Valid {
		t.AuthToken = authToken.String
	}

	return &t, nil
}

// List returns recent tasks, optionally filtered by status.
func (s *Store) List(ctx context.Context, status string, limit int) ([]*Task, error) {
	if limit <= 0 {
		limit = 50
	}

	var rows *sql.Rows
	var err error

	if status != "" {
		query := `
		SELECT id, task_id, profile_id, reference_id, task_type, target_url,
		       auth_token, payload, status, retry_count, max_retries,
		       last_error, next_retry_at, created_at, updated_at
		FROM outbox_tasks
		WHERE status = ?
		ORDER BY id DESC
		LIMIT ?
		`
		rows, err = s.db.QueryContext(ctx, query, status, limit)
	} else {
		query := `
		SELECT id, task_id, profile_id, reference_id, task_type, target_url,
		       auth_token, payload, status, retry_count, max_retries,
		       last_error, next_retry_at, created_at, updated_at
		FROM outbox_tasks
		ORDER BY id DESC
		LIMIT ?
		`
		rows, err = s.db.QueryContext(ctx, query, limit)
	}

	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tasks []*Task
	for rows.Next() {
		var t Task
		var rawType, rawStatus string
		var lastErr, authToken sql.NullString

		if err := rows.Scan(
			&t.ID, &t.TaskID, &t.ProfileID, &t.ReferenceID, &rawType, &t.TargetURL,
			&authToken, &t.Payload, &rawStatus, &t.RetryCount, &t.MaxRetries,
			&lastErr, &t.NextRetryAt, &t.CreatedAt, &t.UpdatedAt,
		); err != nil {
			return nil, err
		}

		t.TaskType = TaskType(rawType)
		t.Status = TaskStatus(rawStatus)
		if lastErr.Valid {
			t.LastError = lastErr.String
		}
		if authToken.Valid {
			t.AuthToken = authToken.String
		}

		tasks = append(tasks, &t)
	}

	return tasks, rows.Err()
}

// Stats returns the count of tasks grouped by status.
func (s *Store) Stats(ctx context.Context) (map[string]int64, error) {
	query := `SELECT status, COUNT(*) FROM outbox_tasks GROUP BY status`
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	stats := make(map[string]int64)
	for rows.Next() {
		var status string
		var count int64
		if err := rows.Scan(&status, &count); err != nil {
			return nil, err
		}
		stats[status] = count
	}
	return stats, rows.Err()
}
