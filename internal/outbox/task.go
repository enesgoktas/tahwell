package outbox

import (
	"time"
)

// TaskStatus represents the lifecycle state of a task in the outbox.
type TaskStatus string

const (
	StatusPending    TaskStatus = "PENDING"
	StatusProcessing TaskStatus = "PROCESSING"
	StatusCompleted  TaskStatus = "COMPLETED"
	StatusDeadLetter TaskStatus = "DEAD_LETTER"
)

// TaskType represents the category of the outbound operation.
type TaskType string

const (
	TypeOrderDispatch TaskType = "ORDER_DISPATCH"
	TypeERPCallback   TaskType = "ERP_CALLBACK"
)

// Task represents an item in the transactional outbox retry queue.
type Task struct {
	ID          int64      `json:"id"`
	TaskID      string     `json:"task_id"`
	ProfileID   string     `json:"profile_id"`
	ReferenceID string     `json:"reference_id"`
	TaskType    TaskType   `json:"task_type"`
	TargetURL   string     `json:"target_url"`
	AuthToken   string     `json:"auth_token,omitempty"`
	Payload     string     `json:"payload"`
	Status      TaskStatus `json:"status"`
	RetryCount  int        `json:"retry_count"`
	MaxRetries  int        `json:"max_retries"`
	LastError   string     `json:"last_error,omitempty"`
	NextRetryAt time.Time  `json:"next_retry_at"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}
