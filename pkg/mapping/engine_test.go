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

	t.Run("Two-pass Canonical Order Transformation", func(t *testing.T) {
		input := map[string]any{
			"OrderHeader": map[string]any{
				"OrderNo":       "ORD-99911",
				"PlantCode":     "PLANT-01",
				"DepartureTime": "20.11.2026 10:00",
			},
			"Cargo": map[string]any{
				"WeightInTons": "12.0",
				"PalletCount":  "15",
			},
			"Delivery": map[string]any{
				"Customer": "Target Supermarket",
			},
		}

		canonicalMap, err := engine.Transform(spec, input)
		if err != nil {
			t.Fatalf("Transform to canonical map failed: %v", err)
		}
		if canonicalMap["reference_id"] != "ORD-99911" {
			t.Errorf("expected ORD-99911, got %v", canonicalMap["reference_id"])
		}

		// Outbound conversion
		outboundSpecYAML := `
name: "canonical_to_carrier"
version: "1.0.0"
rules:
  - source: "reference_id"
    target: "carrier_ref"
  - source: "cargo.gross_weight_kg"
    target: "weight"
`
		outboundSpec, err := LoadSpecFromYAML([]byte(outboundSpecYAML))
		if err != nil {
			t.Fatalf("failed loading outbound spec: %v", err)
		}

		outboundPayload, err := engine.Transform(outboundSpec, canonicalMap)
		if err != nil {
			t.Fatalf("Transform to outbound failed: %v", err)
		}
		if outboundPayload["carrier_ref"] != "ORD-99911" {
			t.Errorf("expected carrier_ref ORD-99911, got %v", outboundPayload["carrier_ref"])
		}
		if outboundPayload["weight"] != 12000.0 {
			t.Errorf("expected weight 12000, got %v", outboundPayload["weight"])
		}
	})

	t.Run("Status Event Two-Pass Transformation", func(t *testing.T) {
		statusInboundYAML := `
name: "carrier_webhook_to_status"
version: "1.0.0"
rules:
  - source: "trip_code"
    target: "carrier_trip_code"
    required: true
  - source: "order_id"
    target: "order_reference_id"
  - source: "milestone"
    target: "status"
    required: true
  - source: "occurred_at"
    target: "timestamp"
    transform: "date_iso8601"
  - source: "pod_link"
    target: "pod_url"
`
		statusSpec, err := LoadSpecFromYAML([]byte(statusInboundYAML))
		if err != nil {
			t.Fatalf("failed loading status spec: %v", err)
		}

		carrierWebhook := map[string]any{
			"trip_code":   "TRIP-88899",
			"order_id":    "ORD-90412",
			"milestone":   "DELIVERED",
			"occurred_at": "20.11.2026 16:45:00",
			"pod_link":    "https://storage.carrier.example/pod/88899.pdf",
		}

		statusMap, err := engine.Transform(statusSpec, carrierWebhook)
		if err != nil {
			t.Fatalf("Transform to canonical status failed: %v", err)
		}
		if statusMap["carrier_trip_code"] != "TRIP-88899" {
			t.Errorf("expected TRIP-88899, got %v", statusMap["carrier_trip_code"])
		}
		if statusMap["status"] != "DELIVERED" {
			t.Errorf("expected DELIVERED, got %v", statusMap["status"])
		}
		if statusMap["pod_url"] != "https://storage.carrier.example/pod/88899.pdf" {
			t.Errorf("expected POD URL, got %v", statusMap["pod_url"])
		}

		// Transform from canonical status map to ERP callback payload
		erpCallbackYAML := `
name: "status_to_erp_callback"
version: "1.0.0"
rules:
  - source: "order_reference_id"
    target: "erp_order_number"
  - source: "status"
    target: "delivery_status"
  - source: "pod_url"
    target: "proof_of_delivery"
`
		erpSpec, err := LoadSpecFromYAML([]byte(erpCallbackYAML))
		if err != nil {
			t.Fatalf("failed loading erp callback spec: %v", err)
		}

		erpPayload, err := engine.Transform(erpSpec, statusMap)
		if err != nil {
			t.Fatalf("Transform to ERP callback failed: %v", err)
		}
		if erpPayload["erp_order_number"] != "ORD-90412" {
			t.Errorf("expected erp_order_number ORD-90412, got %v", erpPayload["erp_order_number"])
		}
		if erpPayload["delivery_status"] != "DELIVERED" {
			t.Errorf("expected delivery_status DELIVERED, got %v", erpPayload["delivery_status"])
		}
	})
}
