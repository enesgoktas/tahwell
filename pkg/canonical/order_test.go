package canonical

import (
	"testing"
	"time"
)

func TestTransportOrder_Validate(t *testing.T) {
	validOrder := func() TransportOrder {
		return TransportOrder{
			ID:          "ord-001",
			ReferenceID: "TMK-90412",
			ShipperName: "ACME Global",
			Status:      StatusDraft,
			CreatedAt:   time.Now().UTC(),
			Pickup: Location{
				FacilityCode: "PLANT-01",
				CompanyName:  "ACME Manufacturing Facility",
				Address:      "Ovaakça Mah. Sanayi Cad. No:4",
				City:         "Bursa",
				ScheduledAt:  time.Now().Add(24 * time.Hour),
			},
			Delivery: Location{
				CompanyName: "Migros Gebze Dağıtım Merkezi",
				Address:     "GOSB 400. Sokak No:12",
				City:        "Kocaeli",
				ScheduledAt: time.Now().Add(48 * time.Hour),
			},
			Cargo: CargoDetails{
				PackageType:   PackagePallet,
				Quantity:      24,
				GrossWeightKg: 18500.0,
			},
		}
	}

	t.Run("Valid Order passes validation", func(t *testing.T) {
		ord := validOrder()
		if err := ord.Validate(); err != nil {
			t.Fatalf("expected valid order, got error: %v", err)
		}
	})

	t.Run("Missing ReferenceID returns error", func(t *testing.T) {
		ord := validOrder()
		ord.ReferenceID = "   "
		err := ord.Validate()
		if err == nil {
			t.Fatal("expected error for missing reference_id, got nil")
		}
	})

	t.Run("Missing Pickup Address & Facility returns error", func(t *testing.T) {
		ord := validOrder()
		ord.Pickup.Address = ""
		ord.Pickup.FacilityCode = ""
		err := ord.Validate()
		if err == nil {
			t.Fatal("expected error for missing pickup address, got nil")
		}
	})

	t.Run("Missing Delivery Address & Company returns error", func(t *testing.T) {
		ord := validOrder()
		ord.Delivery.Address = ""
		ord.Delivery.CompanyName = ""
		err := ord.Validate()
		if err == nil {
			t.Fatal("expected error for missing delivery address, got nil")
		}
	})

	t.Run("Zero or negative Quantity returns error", func(t *testing.T) {
		ord := validOrder()
		ord.Cargo.Quantity = 0
		if err := ord.Validate(); err == nil {
			t.Fatal("expected error for zero quantity, got nil")
		}

		ord.Cargo.Quantity = -5
		if err := ord.Validate(); err == nil {
			t.Fatal("expected error for negative quantity, got nil")
		}
	})

	t.Run("Zero or negative GrossWeight returns error", func(t *testing.T) {
		ord := validOrder()
		ord.Cargo.GrossWeightKg = 0
		if err := ord.Validate(); err == nil {
			t.Fatal("expected error for zero weight, got nil")
		}

		ord.Cargo.GrossWeightKg = -100.5
		if err := ord.Validate(); err == nil {
			t.Fatal("expected error for negative weight, got nil")
		}
	})
}
