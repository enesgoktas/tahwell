package mapping

import (
	"testing"
)

func TestEngine_Transform(t *testing.T) {
	yamlSpec := `
name: "soap_to_canonical_test"
version: "1.0.0"
lookups:
  plants:
    "PLANT-01": "Central Manufacturing Facility, Industrial Zone"
    "HUB-02": "Western Logistics Terminal"
rules:
  - source: "OrderHeader.OrderNo"
    target: "reference_id"
    required: true

  - source: "OrderHeader.PlantCode"
    target: "pickup.address"
    lookup: "plants"
    required: true

  - source: "OrderHeader.DepartureTime"
    target: "pickup.scheduled_at"
    transform: "date_iso8601"

  - source: "Cargo.WeightInTons"
    target: "cargo.gross_weight_kg"
    transform: "multiply:1000"

  - source: "Cargo.PalletCount"
    target: "cargo.quantity"
    transform: "to_int"

  - source: "Delivery.Customer"
    target: "delivery.company_name"
    transform: "trim"

  - source: "Delivery.Address"
    target: "delivery.address"
    default: "Central Distribution Facility"
`

	spec, err := LoadSpecFromYAML([]byte(yamlSpec))
	if err != nil {
		t.Fatalf("failed to load YAML spec: %v", err)
	}

	engine := NewEngine()

	t.Run("Full successful transformation", func(t *testing.T) {
		input := map[string]any{
			"OrderHeader": map[string]any{
				"OrderNo":       "ORD-90412",
				"PlantCode":     "PLANT-01",
				"DepartureTime": "20.11.2026 09:00",
			},
			"Cargo": map[string]any{
				"WeightInTons": "18.5",
				"PalletCount":  "24",
			},
			"Delivery": map[string]any{
				"Customer": "  Retail Supermarket Chain  ",
			},
		}

		out, err := engine.Transform(spec, input)
		if err != nil {
			t.Fatalf("expected successful transform, got: %v", err)
		}

		if out["reference_id"] != "ORD-90412" {
			t.Errorf("expected reference_id ORD-90412, got %v", out["reference_id"])
		}

		// Check lookup applied
		pickup, ok := out["pickup"].(map[string]any)
		if !ok {
			t.Fatalf("expected pickup object, got %T", out["pickup"])
		}
		if pickup["address"] != "Central Manufacturing Facility, Industrial Zone" {
			t.Errorf("lookup failed, got address: %v", pickup["address"])
		}

		// Check date transform
		if pickup["scheduled_at"] != "2026-11-20T09:00:00Z" {
			t.Errorf("date transform mismatch: %v", pickup["scheduled_at"])
		}

		// Check multiplier
		cargo, ok := out["cargo"].(map[string]any)
		if !ok {
			t.Fatalf("expected cargo object, got %T", out["cargo"])
		}
		if cargo["gross_weight_kg"] != 18500.0 {
			t.Errorf("expected 18500.0 kg, got %v", cargo["gross_weight_kg"])
		}
		if cargo["quantity"] != 24 {
			t.Errorf("expected quantity 24, got %v", cargo["quantity"])
		}

		// Check default fallback
		delivery, ok := out["delivery"].(map[string]any)
		if !ok {
			t.Fatalf("expected delivery object, got %T", out["delivery"])
		}
		if delivery["company_name"] != "Retail Supermarket Chain" {
			t.Errorf("expected trimmed customer, got %q", delivery["company_name"])
		}
		if delivery["address"] != "Central Distribution Facility" {
			t.Errorf("expected default address, got %v", delivery["address"])
		}
	})

	t.Run("Missing required field returns error", func(t *testing.T) {
		input := map[string]any{
			"OrderHeader": map[string]any{
				// OrderNo missing!
				"PlantCode": "BURSA-01",
			},
		}

		_, err := engine.Transform(spec, input)
		if err == nil {
			t.Fatal("expected error for missing required field, got nil")
		}
	})
}
