package canonical

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Common validation errors.
var (
	ErrMissingReferenceID   = errors.New("reference_id is required")
	ErrMissingPickupAddress = errors.New("pickup location requires an address or facility code")
	ErrMissingDeliveryAddr  = errors.New("delivery location requires an address or company name")
	ErrInvalidQuantity      = errors.New("cargo quantity must be greater than zero")
	ErrInvalidWeight        = errors.New("cargo gross weight must be greater than zero")
)

// OrderStatus represents the lifecycle state of a transport order.
type OrderStatus string

const (
	StatusDraft      OrderStatus = "DRAFT"
	StatusPending    OrderStatus = "PENDING"
	StatusDispatched OrderStatus = "DISPATCHED"
	StatusFailed     OrderStatus = "FAILED"
)

// PackageType represents standard packaging categories.
type PackageType string

const (
	PackagePallet    PackageType = "PALLET"
	PackageBox       PackageType = "BOX"
	PackageBulk      PackageType = "BULK"
	PackageContainer PackageType = "CONTAINER"
)

// TemperatureRegime defines temperature control constraints.
type TemperatureRegime string

const (
	TempDry     TemperatureRegime = "DRY"
	TempChilled TemperatureRegime = "CHILLED"
	TempFrozen  TemperatureRegime = "FROZEN"
)

// Location specifies pickup or dropoff point details.
type Location struct {
	FacilityCode string    `json:"facility_code,omitempty"`
	CompanyName  string    `json:"company_name"`
	TaxID        string    `json:"tax_id,omitempty"`
	Address      string    `json:"address"`
	District     string    `json:"district,omitempty"`
	City         string    `json:"city"`
	Country      string    `json:"country,omitempty"`
	ContactName  string    `json:"contact_name,omitempty"`
	ContactPhone string    `json:"contact_phone,omitempty"`
	ScheduledAt  time.Time `json:"scheduled_at"`
}

// CargoDetails holds volumetric and physical specs of the items.
type CargoDetails struct {
	PackageType       PackageType       `json:"package_type"`
	Quantity          int               `json:"quantity"`
	GrossWeightKg     float64           `json:"gross_weight_kg"`
	VolumeM3          float64           `json:"volume_m3,omitempty"`
	TemperatureRegime TemperatureRegime `json:"temperature_regime,omitempty"`
	Notes             string            `json:"notes,omitempty"`
}

// VehicleReqs defines required transport vehicle characteristics.
type VehicleReqs struct {
	VehicleType string   `json:"vehicle_type,omitempty"` // e.g. "TENTELI_TIR", "KIRKAYAK", "KAMYONET"
	Features    []string `json:"features,omitempty"`     // e.g. ["TAIL_LIFT", "SIDE_LOADING"]
}

// TransportOrder is the canonical, technology-neutral representation
// of a freight / transport request in Tahwell.
type TransportOrder struct {
	ID          string       `json:"id"`
	ReferenceID string       `json:"reference_id"` // Shipper's PO or order code
	ShipperName string       `json:"shipper_name"`
	Status      OrderStatus  `json:"status"`
	CreatedAt   time.Time    `json:"created_at"`
	Pickup      Location     `json:"pickup"`
	Delivery    Location     `json:"delivery"`
	Cargo       CargoDetails `json:"cargo"`
	Vehicle     VehicleReqs  `json:"vehicle,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
}

// Validate checks business invariants of the canonical order.
func (o *TransportOrder) Validate() error {
	var errs []string

	if strings.TrimSpace(o.ReferenceID) == "" {
		errs = append(errs, ErrMissingReferenceID.Error())
	}
	if strings.TrimSpace(o.Pickup.Address) == "" && strings.TrimSpace(o.Pickup.FacilityCode) == "" {
		errs = append(errs, ErrMissingPickupAddress.Error())
	}
	if strings.TrimSpace(o.Delivery.Address) == "" && strings.TrimSpace(o.Delivery.CompanyName) == "" {
		errs = append(errs, ErrMissingDeliveryAddr.Error())
	}
	if o.Cargo.Quantity <= 0 {
		errs = append(errs, ErrInvalidQuantity.Error())
	}
	if o.Cargo.GrossWeightKg <= 0 {
		errs = append(errs, ErrInvalidWeight.Error())
	}

	if len(errs) > 0 {
		return fmt.Errorf("canonical validation failed: %s", strings.Join(errs, "; "))
	}
	return nil
}
