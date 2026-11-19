package mapping

import (
	"os"
	"testing"
)

func TestParseJSON(t *testing.T) {
	raw := []byte(`{
		"order_id": "ORD-1234",
		"quantity": 10,
		"cargo": {
			"weight_kg": 1500.5,
			"dangerous": false
		}
	}`)

	m, err := ParseJSON(raw)
	if err != nil {
		t.Fatalf("ParseJSON failed: %v", err)
	}

	if m["order_id"] != "ORD-1234" {
		t.Errorf("expected ORD-1234, got %v", m["order_id"])
	}
	cargo, ok := m["cargo"].(map[string]any)
	if !ok {
		t.Fatalf("expected cargo to be a map, got %T", m["cargo"])
	}
	if w, ok := cargo["weight_kg"].(float64); !ok || w != 1500.5 {
		t.Errorf("expected weight_kg 1500.5, got %v", cargo["weight_kg"])
	}
}

func TestParseXML_SOAP(t *testing.T) {
	soapContent, err := os.ReadFile("../../testdata/sample_soap_order.xml")
	if err != nil {
		t.Fatalf("failed reading sample soap xml: %v", err)
	}

	m, err := ParseXML(soapContent)
	if err != nil {
		t.Fatalf("ParseXML failed: %v", err)
	}

	// Verify SOAP body elevation
	if m["OrderNumber"] != "ORD-90412" {
		t.Errorf("expected OrderNumber ORD-90412, got %v", m["OrderNumber"])
	}
	if m["PlantCode"] != "PLANT-01" {
		t.Errorf("expected PlantCode PLANT-01, got %v", m["PlantCode"])
	}
	if m["TotalKg"] != "18500" {
		t.Errorf("expected TotalKg 18500, got %v", m["TotalKg"])
	}
}

func TestParseAuto(t *testing.T) {
	jsonRaw := []byte(`{"status": "IN_TRANSIT"}`)
	xmlRaw := []byte(`<StatusUpdate><Status>DELIVERED</Status></StatusUpdate>`)

	// 1. By Content-Type
	m1, err := ParseAuto("application/json; charset=utf-8", jsonRaw)
	if err != nil || m1["status"] != "IN_TRANSIT" {
		t.Errorf("ParseAuto JSON by CT failed: %v", err)
	}

	m2, err := ParseAuto("text/xml", xmlRaw)
	if err != nil || m2["Status"] != "DELIVERED" {
		t.Errorf("ParseAuto XML by CT failed: %v", err)
	}

	// 2. By Sniffing (empty Content-Type)
	m3, err := ParseAuto("", jsonRaw)
	if err != nil || m3["status"] != "IN_TRANSIT" {
		t.Errorf("ParseAuto sniff JSON failed: %v", err)
	}

	m4, err := ParseAuto("", xmlRaw)
	if err != nil || m4["Status"] != "DELIVERED" {
		t.Errorf("ParseAuto sniff XML failed: %v", err)
	}
}
