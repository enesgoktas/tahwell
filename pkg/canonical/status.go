package canonical

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrMissingStatusIdentifiers = errors.New("at least one of order_reference_id or carrier_trip_code is required")
	ErrInvalidDeliveryStatus    = errors.New("invalid or empty delivery status")
	ErrMissingTimestamp         = errors.New("status event timestamp is required")
)

// DeliveryStatus represents standardized freight and parcel milestone statuses.
type DeliveryStatus string

const (
	DeliveryStatusCreated        DeliveryStatus = "CREATED"
	DeliveryStatusAssigned       DeliveryStatus = "ASSIGNED"
	DeliveryStatusInTransit      DeliveryStatus = "IN_TRANSIT"
	DeliveryStatusOutForDelivery DeliveryStatus = "OUT_FOR_DELIVERY"
	DeliveryStatusDelivered      DeliveryStatus = "DELIVERED"
	DeliveryStatusFailed         DeliveryStatus = "FAILED"
	DeliveryStatusCancelled      DeliveryStatus = "CANCELLED"
)

// IsValid checks if the status is within the standard canonical lifecycle.
func (s DeliveryStatus) IsValid() bool {
	switch s {
	case DeliveryStatusCreated, DeliveryStatusAssigned, DeliveryStatusInTransit, DeliveryStatusOutForDelivery, DeliveryStatusDelivered, DeliveryStatusFailed, DeliveryStatusCancelled:
		return true
	default:
		return false
	}
}

// StatusEvent is the canonical, technology-neutral representation
// of a shipment status or milestone update in Tahwell.
type StatusEvent struct {
	EventID          string         `json:"event_id"`
	OrderReferenceID string         `json:"order_reference_id,omitempty"` // Shipper's PO / Order ID
	CarrierTripCode  string         `json:"carrier_trip_code,omitempty"`  // Carrier's internal tracking/trip code
	Status           DeliveryStatus `json:"status"`
	Timestamp        time.Time      `json:"timestamp"`
	Location         string         `json:"location,omitempty"`
	DriverPhone      string         `json:"driver_phone,omitempty"`
	PODURL           string         `json:"pod_url,omitempty"` // Proof of Delivery doc / photo link
	ExceptionReason  string         `json:"exception_reason,omitempty"`
	Notes            string         `json:"notes,omitempty"`
	Metadata         map[string]any `json:"metadata,omitempty"`
}

// Validate checks business invariants of the canonical status event.
func (e *StatusEvent) Validate() error {
	var errs []string

	if strings.TrimSpace(e.OrderReferenceID) == "" && strings.TrimSpace(e.CarrierTripCode) == "" {
		errs = append(errs, ErrMissingStatusIdentifiers.Error())
	}
	if !e.Status.IsValid() {
		errs = append(errs, fmt.Sprintf("%s: %q", ErrInvalidDeliveryStatus.Error(), e.Status))
	}
	if e.Timestamp.IsZero() {
		errs = append(errs, ErrMissingTimestamp.Error())
	}

	if len(errs) > 0 {
		return fmt.Errorf("canonical status validation failed: %s", strings.Join(errs, "; "))
	}
	return nil
}

// ToMap converts StatusEvent to a generic map[string]any.
func (e *StatusEvent) ToMap() (map[string]any, error) {
	data, err := json.Marshal(e)
	if err != nil {
		return nil, fmt.Errorf("failed marshaling status event: %w", err)
	}
	var res map[string]any
	if err := json.Unmarshal(data, &res); err != nil {
		return nil, fmt.Errorf("failed unmarshaling status event to map: %w", err)
	}
	return res, nil
}

// StatusEventFromMap creates and validates a StatusEvent from a generic map.
func StatusEventFromMap(m map[string]any) (*StatusEvent, error) {
	data, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("failed serializing map to status event: %w", err)
	}
	var event StatusEvent
	if err := json.Unmarshal(data, &event); err != nil {
		return nil, fmt.Errorf("failed deserializing status event: %w", err)
	}
	if err := event.Validate(); err != nil {
		return nil, err
	}
	return &event, nil
}
