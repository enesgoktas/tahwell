package canonical

import (
	"testing"
	"time"
)

func TestStatusEvent_Validate(t *testing.T) {
	now := time.Now().UTC()

	tests := []struct {
		name    string
		event   StatusEvent
		wantErr bool
	}{
		{
			name: "Valid event with OrderReferenceID",
			event: StatusEvent{
				EventID:          "evt-101",
				OrderReferenceID: "ORD-90412",
				Status:           DeliveryStatusInTransit,
				Timestamp:        now,
				Location:         "Istanbul",
			},
			wantErr: false,
		},
		{
			name: "Valid event with CarrierTripCode only",
			event: StatusEvent{
				EventID:         "evt-102",
				CarrierTripCode: "TRIP-88899",
				Status:          DeliveryStatusDelivered,
				Timestamp:       now,
				PODURL:          "https://storage.carrier.com/pod/123.jpg",
			},
			wantErr: false,
		},
		{
			name: "Missing both OrderReferenceID and CarrierTripCode",
			event: StatusEvent{
				EventID:   "evt-103",
				Status:    DeliveryStatusDelivered,
				Timestamp: now,
			},
			wantErr: true,
		},
		{
			name: "Invalid status value",
			event: StatusEvent{
				OrderReferenceID: "ORD-90412",
				Status:           "SOME_RANDOM_STATUS",
				Timestamp:        now,
			},
			wantErr: true,
		},
		{
			name: "Missing timestamp",
			event: StatusEvent{
				OrderReferenceID: "ORD-90412",
				Status:           DeliveryStatusCreated,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.event.Validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr = %v", err, tt.wantErr)
			}
		})
	}
}

func TestStatusEvent_MapRoundTrip(t *testing.T) {
	original := &StatusEvent{
		EventID:          "evt-200",
		OrderReferenceID: "ORD-12345",
		CarrierTripCode:  "CARRIER-TRIP-99",
		Status:           DeliveryStatusDelivered,
		Timestamp:        time.Date(2026, 11, 20, 14, 30, 0, 0, time.UTC),
		Location:         "Distribution Center North",
		DriverPhone:      "+905551234567",
		PODURL:           "https://carrier.example/pod/99.pdf",
		Notes:            "Delivered and signed by warehouse supervisor",
	}

	m, err := original.ToMap()
	if err != nil {
		t.Fatalf("ToMap() error = %v", err)
	}

	restored, err := StatusEventFromMap(m)
	if err != nil {
		t.Fatalf("StatusEventFromMap() error = %v", err)
	}

	if restored.OrderReferenceID != original.OrderReferenceID {
		t.Errorf("OrderReferenceID mismatch: got %q, want %q", restored.OrderReferenceID, original.OrderReferenceID)
	}
	if restored.Status != original.Status {
		t.Errorf("Status mismatch: got %v, want %v", restored.Status, original.Status)
	}
	if restored.CarrierTripCode != original.CarrierTripCode {
		t.Errorf("CarrierTripCode mismatch: got %q, want %q", restored.CarrierTripCode, original.CarrierTripCode)
	}
}
