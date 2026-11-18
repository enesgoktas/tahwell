package ledger

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// Event represents an immutable audit log entry in the Tahwell event ledger.
type Event struct {
	ID                 int64     `json:"id"`
	EventID            string    `json:"event_id"`
	Shipper            string    `json:"shipper"`
	Carrier            string    `json:"carrier"`
	ReferenceID        string    `json:"reference_id"`
	Direction          string    `json:"direction"` // e.g. "INBOUND", "OUTBOUND"
	SourceFormat       string    `json:"source_format"` // e.g. "SOAP_XML"
	TargetFormat       string    `json:"target_format"` // e.g. "REST_JSON"
	RawPayload         string    `json:"raw_payload"`
	TransformedPayload string    `json:"transformed_payload,omitempty"`
	Status             string    `json:"status"` // e.g. "PROCESSED", "FAILED"
	CarrierTripCode    string    `json:"carrier_trip_code,omitempty"`
	ErrorMessage       string    `json:"error_message,omitempty"`
	DurationMs         int64     `json:"duration_ms"`
	CreatedAt          time.Time `json:"created_at"`
}

// Ledger manages the audit log database.
type Ledger struct {
	db *sql.DB
}

// New opens or creates an SQLite-backed event ledger.
func New(dbPath string) (*Ledger, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed opening sqlite ledger: %w", err)
	}

	createTableQuery := `
	CREATE TABLE IF NOT EXISTS audit_events (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		event_id TEXT NOT NULL,
		shipper TEXT NOT NULL,
		carrier TEXT NOT NULL,
		reference_id TEXT NOT NULL,
		direction TEXT NOT NULL,
		source_format TEXT NOT NULL,
		target_format TEXT NOT NULL,
		raw_payload TEXT NOT NULL,
		transformed_payload TEXT,
		status TEXT NOT NULL,
		carrier_trip_code TEXT,
		error_message TEXT,
		duration_ms INTEGER NOT NULL,
		created_at DATETIME NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_events_ref ON audit_events(reference_id);
	CREATE INDEX IF NOT EXISTS idx_events_created ON audit_events(created_at);
	`
	if _, err := db.Exec(createTableQuery); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed initializing ledger table: %w", err)
	}

	return &Ledger{db: db}, nil
}

// Record inserts a new event into the ledger.
func (l *Ledger) Record(ctx context.Context, e *Event) error {
	if e.CreatedAt.IsZero() {
		e.CreatedAt = time.Now().UTC()
	}

	query := `
	INSERT INTO audit_events (
		event_id, shipper, carrier, reference_id, direction,
		source_format, target_format, raw_payload, transformed_payload,
		status, carrier_trip_code, error_message, duration_ms, created_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`

	res, err := l.db.ExecContext(ctx, query,
		e.EventID, e.Shipper, e.Carrier, e.ReferenceID, e.Direction,
		e.SourceFormat, e.TargetFormat, e.RawPayload, e.TransformedPayload,
		e.Status, e.CarrierTripCode, e.ErrorMessage, e.DurationMs, e.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to record audit event: %w", err)
	}

	id, err := res.LastInsertId()
	if err == nil {
		e.ID = id
	}
	return nil
}

// ListRecent retrieves the last N recorded events.
func (l *Ledger) ListRecent(ctx context.Context, limit int) ([]Event, error) {
	if limit <= 0 {
		limit = 50
	}

	query := `
	SELECT id, event_id, shipper, carrier, reference_id, direction,
	       source_format, target_format, raw_payload, transformed_payload,
	       status, carrier_trip_code, error_message, duration_ms, created_at
	FROM audit_events
	ORDER BY id DESC
	LIMIT ?
	`

	rows, err := l.db.QueryContext(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("querying recent events failed: %w", err)
	}
	defer rows.Close()

	var events []Event
	for rows.Next() {
		var e Event
		var tripCode, errMsg, transformed sql.NullString

		if err := rows.Scan(
			&e.ID, &e.EventID, &e.Shipper, &e.Carrier, &e.ReferenceID, &e.Direction,
			&e.SourceFormat, &e.TargetFormat, &e.RawPayload, &transformed,
			&e.Status, &tripCode, &errMsg, &e.DurationMs, &e.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scanning event row failed: %w", err)
		}

		if tripCode.Valid {
			e.CarrierTripCode = tripCode.String
		}
		if errMsg.Valid {
			e.ErrorMessage = errMsg.String
		}
		if transformed.Valid {
			e.TransformedPayload = transformed.String
		}
		events = append(events, e)
	}

	return events, nil
}

// Close closes the database connection.
func (l *Ledger) Close() error {
	return l.db.Close()
}
