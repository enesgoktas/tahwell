package outbox

import (
	"context"
	"encoding/json"
	"log"
	"math"
	"math/rand"
	"sync"
	"time"

	"github.com/goktasenes/tahwell/internal/ledger"
)

// HTTPDispatcher defines an interface for dispatching generic payloads to endpoints.
type HTTPDispatcher interface {
	DispatchGeneric(ctx context.Context, endpointURL, token string, payload any) (map[string]any, error)
}

// WorkerConfig holds configuration options for the outbox retry worker.
type WorkerConfig struct {
	PollInterval time.Duration
	BatchSize    int
	BaseBackoff  time.Duration
	MaxBackoff   time.Duration
}

// DefaultWorkerConfig returns production defaults for retry intervals.
func DefaultWorkerConfig() WorkerConfig {
	return WorkerConfig{
		PollInterval: 1 * time.Second,
		BatchSize:    20,
		BaseBackoff:  3 * time.Second,
		MaxBackoff:   5 * time.Minute,
	}
}

// Worker executes background polling and exponential-backoff retries for failed tasks.
type Worker struct {
	store      *Store
	ledger     *ledger.Ledger
	dispatcher HTTPDispatcher
	config     WorkerConfig
	stopChan   chan struct{}
	wg         sync.WaitGroup
	running    bool
	mu         sync.Mutex
}

// NewWorker initializes a new outbox retry worker.
func NewWorker(store *Store, l *ledger.Ledger, dispatcher HTTPDispatcher, cfg *WorkerConfig) *Worker {
	c := DefaultWorkerConfig()
	if cfg != nil {
		if cfg.PollInterval > 0 {
			c.PollInterval = cfg.PollInterval
		}
		if cfg.BatchSize > 0 {
			c.BatchSize = cfg.BatchSize
		}
		if cfg.BaseBackoff > 0 {
			c.BaseBackoff = cfg.BaseBackoff
		}
		if cfg.MaxBackoff > 0 {
			c.MaxBackoff = cfg.MaxBackoff
		}
	}

	return &Worker{
		store:      store,
		ledger:     l,
		dispatcher: dispatcher,
		config:     c,
		stopChan:   make(chan struct{}),
	}
}

// Start launches the background worker goroutine.
func (w *Worker) Start(ctx context.Context) {
	w.mu.Lock()
	if w.running {
		w.mu.Unlock()
		return
	}
	w.running = true
	w.stopChan = make(chan struct{})
	w.mu.Unlock()

	w.wg.Add(1)
	go w.run(ctx)
}

// Stop gracefully halts the background worker and waits for active tasks to conclude.
func (w *Worker) Stop() {
	w.mu.Lock()
	if !w.running {
		w.mu.Unlock()
		return
	}
	w.running = false
	close(w.stopChan)
	w.mu.Unlock()

	w.wg.Wait()
}

func (w *Worker) run(ctx context.Context) {
	defer w.wg.Done()
	ticker := time.NewTicker(w.config.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-w.stopChan:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.ProcessBatch(ctx)
		}
	}
}

// ProcessBatch fetches due tasks and attempts delivery.
func (w *Worker) ProcessBatch(ctx context.Context) int {
	tasks, err := w.store.FetchDue(ctx, w.config.BatchSize)
	if err != nil {
		log.Printf("[OUTBOX ERROR] Failed fetching due tasks: %v", err)
		return 0
	}

	for _, task := range tasks {
		select {
		case <-w.stopChan:
			return 0
		case <-ctx.Done():
			return 0
		default:
			w.processTask(ctx, task)
		}
	}

	return len(tasks)
}

func (w *Worker) processTask(ctx context.Context, task *Task) {
	start := time.Now()

	// Parse stored payload JSON
	var payloadMap any
	if err := json.Unmarshal([]byte(task.Payload), &payloadMap); err != nil {
		payloadMap = task.Payload
	}

	respMap, err := w.dispatcher.DispatchGeneric(ctx, task.TargetURL, task.AuthToken, payloadMap)
	duration := time.Since(start)

	if err == nil {
		// Delivery succeeded!
		_ = w.store.MarkCompleted(ctx, task.TaskID)

		tripID := ""
		if respMap != nil {
			if t, ok := respMap["trip_id"].(string); ok {
				tripID = t
			} else if t, ok := respMap["carrier_trip_code"].(string); ok {
				tripID = t
			}
		}

		w.recordAudit(ctx, "evt-retry-"+task.TaskID, task.ReferenceID, "OUTBOX_RETRY", task.Payload, "PROCESSED", tripID, "", duration)
		return
	}

	// Delivery failed
	errMsg := err.Error()
	newRetryCount := task.RetryCount + 1

	if newRetryCount >= task.MaxRetries {
		// Exhausted retries -> Dead Letter
		_ = w.store.MarkDeadLetter(ctx, task.TaskID, errMsg)
		w.recordAudit(ctx, "evt-dead-"+task.TaskID, task.ReferenceID, "OUTBOX_DEAD_LETTER", task.Payload, "DEAD_LETTER", "", errMsg, duration)
	} else {
		// Schedule next retry with exponential backoff
		backoff := w.calculateBackoff(task.RetryCount)
		nextRetryAt := time.Now().UTC().Add(backoff)
		_ = w.store.MarkFailed(ctx, task.TaskID, errMsg, nextRetryAt)
		w.recordAudit(ctx, "evt-retry-"+task.TaskID, task.ReferenceID, "OUTBOX_RETRY_SCHEDULED", task.Payload, "PENDING_RETRY", "", errMsg, duration)
	}
}

func (w *Worker) calculateBackoff(retryCount int) time.Duration {
	base := float64(w.config.BaseBackoff)
	multiplier := math.Pow(2, float64(retryCount))
	backoff := base * multiplier

	if backoff > float64(w.config.MaxBackoff) {
		backoff = float64(w.config.MaxBackoff)
	}

	// ±15% jitter to prevent thundering herd
	jitter := (rand.Float64()*0.3 - 0.15) * backoff
	total := time.Duration(backoff + jitter)
	if total < w.config.BaseBackoff {
		total = w.config.BaseBackoff
	}
	return total
}

func (w *Worker) recordAudit(ctx context.Context, eventID, refID, direction, rawPayload, status, tripCode, errMsg string, duration time.Duration) {
	if w.ledger == nil {
		return
	}
	event := &ledger.Event{
		EventID:         eventID,
		Shipper:         "Tahwell Outbox",
		Carrier:         "Target Endpoint",
		ReferenceID:     refID,
		Direction:       direction,
		SourceFormat:    "OUTBOX_TASK",
		TargetFormat:    "REST_JSON",
		RawPayload:      rawPayload,
		Status:          status,
		CarrierTripCode: tripCode,
		ErrorMessage:    errMsg,
		DurationMs:      duration.Milliseconds(),
		CreatedAt:       time.Now().UTC(),
	}
	_ = w.ledger.Record(ctx, event)
}
