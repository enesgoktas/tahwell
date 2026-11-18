package soap

import (
	"strings"
	"testing"
)

const sampleSOAPXML = `<?xml version="1.0" encoding="utf-8"?>
<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/" xmlns:thw="http://tahwell.io/schema/transport/v1">
  <soap:Body>
    <thw:CreateTransportRequest>
      <thw:OrderNumber>ORD-90412</thw:OrderNumber>
      <thw:DepartureDate>2026-11-20T09:00:00</thw:DepartureDate>
      <thw:PlantCode>PLANT-01</thw:PlantCode>
      <thw:CustomerTitle>Distribution Hub North</thw:CustomerTitle>
      <thw:DeliveryAddress>Industrial Zone 4th Ave No:12</thw:DeliveryAddress>
      <thw:DeliveryCity>Metropolis</thw:DeliveryCity>
      <thw:PalletCount>24</thw:PalletCount>
      <thw:TotalKg>18500</thw:TotalKg>
      <thw:VehicleType>CURTAINSIDER</thw:VehicleType>
      <thw:SpecialNotes>Standard commercial transport</thw:SpecialNotes>
    </thw:CreateTransportRequest>
  </soap:Body>
</soap:Envelope>`

func TestParseSOAPRequest(t *testing.T) {
	req, err := ParseSOAPRequest([]byte(sampleSOAPXML))
	if err != nil {
		t.Fatalf("unexpected error parsing SOAP: %v", err)
	}

	if req.OrderNumber != "ORD-90412" {
		t.Errorf("expected OrderNumber ORD-90412, got %q", req.OrderNumber)
	}
	if req.PalletCount != 24 {
		t.Errorf("expected PalletCount 24, got %d", req.PalletCount)
	}
	if req.TotalKg != 18500.0 {
		t.Errorf("expected TotalKg 18500, got %f", req.TotalKg)
	}

	plantLookup := map[string]string{
		"PLANT-01": "Central Manufacturing Facility, Plant 1",
	}
	canonicalOrder, err := req.ToCanonical("ACME Global", plantLookup)
	if err != nil {
		t.Fatalf("failed to convert to canonical: %v", err)
	}

	if canonicalOrder.ReferenceID != "ORD-90412" {
		t.Errorf("expected reference_id ORD-90412, got %q", canonicalOrder.ReferenceID)
	}
	if canonicalOrder.Pickup.Address != "Central Manufacturing Facility, Plant 1" {
		t.Errorf("lookup failed, got %q", canonicalOrder.Pickup.Address)
	}
	if canonicalOrder.Cargo.Quantity != 24 {
		t.Errorf("expected quantity 24, got %d", canonicalOrder.Cargo.Quantity)
	}
	if canonicalOrder.Cargo.GrossWeightKg != 18500.0 {
		t.Errorf("expected weight 18500.0, got %f", canonicalOrder.Cargo.GrossWeightKg)
	}
}

func TestBuildSOAPResponse(t *testing.T) {
	data, err := BuildSOAPResponse("ORD-90412", "TRIP-7788", "Shipment created successfully")
	if err != nil {
		t.Fatalf("failed to build SOAP response: %v", err)
	}
	xmlStr := string(data)
	if !strings.Contains(xmlStr, "<thw:CarrierTripCode>TRIP-7788</thw:CarrierTripCode>") {
		t.Errorf("expected CarrierTripCode in response, got:\n%s", xmlStr)
	}
	if !strings.Contains(xmlStr, "<thw:Success>true</thw:Success>") {
		t.Errorf("expected Success true in response, got:\n%s", xmlStr)
	}
}

func TestBuildSOAPFault(t *testing.T) {
	data, err := BuildSOAPFault("Pallet count must be greater than zero")
	if err != nil {
		t.Fatalf("failed to build SOAP fault: %v", err)
	}
	xmlStr := string(data)
	if !strings.Contains(xmlStr, "<faultstring>Pallet count must be greater than zero</faultstring>") {
		t.Errorf("expected faultstring in response, got:\n%s", xmlStr)
	}
}
