package ledger

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestLedger_RecordAndList(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_ledger.db")

	l, err := New(dbPath)
	if err != nil {
		t.Fatalf("failed initializing ledger: %v", err)
	}
	defer l.Close()

	ctx := context.Background()

	event := &Event{
		EventID:            "evt-001",
		Shipper:            "Enterprise Shipper Inc",
		Carrier:            "Carrier Logistics",
		ReferenceID:        "ORD-90412",
		Direction:          "INBOUND",
		SourceFormat:       "SOAP_XML",
		TargetFormat:       "REST_JSON",
		RawPayload:         "<Order>...</Order>",
		TransformedPayload: `{"reference_id":"ORD-90412"}`,
		Status:             "PROCESSED",
		CarrierTripCode:    "TRIP-7711",
		DurationMs:         45,
		CreatedAt:          time.Now().UTC(),
	}

	if err := l.Record(ctx, event); err != nil {
		t.Fatalf("failed recording event: %v", err)
	}

	if event.ID == 0 {
		t.Errorf("expected non-zero ID after insertion, got %d", event.ID)
	}

	events, err := l.ListRecent(ctx, 10)
	if err != nil {
		t.Fatalf("failed listing recent events: %v", err)
	}

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}

	got := events[0]
	if got.ReferenceID != "ORD-90412" {
		t.Errorf("expected reference ORD-90412, got %s", got.ReferenceID)
	}
	if got.CarrierTripCode != "TRIP-7711" {
		t.Errorf("expected CarrierTripCode TRIP-7711, got %s", got.CarrierTripCode)
	}
	if got.Status != "PROCESSED" {
		t.Errorf("expected PROCESSED, got %s", got.Status)
	}
}
