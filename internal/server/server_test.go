package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/goktasenes/tahwell/internal/adapter/rest"
	"github.com/goktasenes/tahwell/internal/ledger"
)

const sampleGenericSOAPXML = `<?xml version="1.0" encoding="utf-8"?>
<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/" xmlns:thw="http://tahwell.io/schema/transport/v1">
  <soap:Body>
    <thw:CreateTransportRequest>
      <thw:OrderNumber>ORD-90412</thw:OrderNumber>
      <thw:DepartureDate>2026-11-20T09:00:00</thw:DepartureDate>
      <thw:PlantCode>PLANT-01</thw:PlantCode>
      <thw:CustomerTitle>Distribution Hub North</thw:CustomerTitle>
      <thw:DeliveryAddress>Industrial Zone 4th Ave No:12 Metropolis</thw:DeliveryAddress>
      <thw:DeliveryCity>Metropolis</thw:DeliveryCity>
      <thw:PalletCount>24</thw:PalletCount>
      <thw:TotalKg>18500</thw:TotalKg>
      <thw:VehicleType>CURTAINSIDER</thw:VehicleType>
      <thw:SpecialNotes>Standard logistics cargo</thw:SpecialNotes>
    </thw:CreateTransportRequest>
  </soap:Body>
</soap:Envelope>`

func TestServer_FullFlow(t *testing.T) {
	// 1. Mock Target Carrier REST Backend
	mockCarrier := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/shipments" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":          "confirmed",
			"trip_id":         "CARRIER-TRIP-9988",
			"estimated_price": 28500.0,
			"created_at":      "2026-11-18T16:00:00Z",
		})
	}))
	defer mockCarrier.Close()

	// 2. Setup Ledger
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "server_test.db")
	l, err := ledger.New(dbPath)
	if err != nil {
		t.Fatalf("failed initializing ledger: %v", err)
	}
	defer l.Close()

	// 3. Setup Tahwell Server
	plantLookups := map[string]string{
		"PLANT-01": "Central Manufacturing Facility, Plant 1",
	}
	carrierClient := rest.NewCarrierClient(mockCarrier.URL, "test_token", nil)
	srv := NewServer(l, carrierClient, "ACME Shipper", "Carrier Express", plantLookups)
	ts := httptest.NewServer(srv)
	defer ts.Close()

	// 4. Test Health Check
	t.Run("Health check", func(t *testing.T) {
		resp, err := http.Get(ts.URL + "/health")
		if err != nil {
			t.Fatalf("health check failed: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("expected 200, got %d", resp.StatusCode)
		}
	})

	// 5. Test Successful Generic Inbound Request
	t.Run("Successful Generic Inbound SOAP to Carrier REST", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/inbound/orders/soap", strings.NewReader(sampleGenericSOAPXML))
		if err != nil {
			t.Fatalf("building request failed: %v", err)
		}
		req.Header.Set("Content-Type", "text/xml")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("expected HTTP 200 OK, got %d", resp.StatusCode)
		}

		body, _ := io.ReadAll(resp.Body)
		bodyStr := string(body)
		if !strings.Contains(bodyStr, "<thw:CarrierTripCode>CARRIER-TRIP-9988</thw:CarrierTripCode>") {
			t.Errorf("expected CarrierTripCode CARRIER-TRIP-9988 in SOAP response, got:\n%s", bodyStr)
		}
		if !strings.Contains(bodyStr, "<thw:Success>true</thw:Success>") {
			t.Errorf("expected Success true in SOAP response, got:\n%s", bodyStr)
		}
	})

	// 6. Test Invalid Request (Validation failure)
	t.Run("Validation error returns SOAP Fault", func(t *testing.T) {
		invalidXML := strings.Replace(sampleGenericSOAPXML, "<thw:PalletCount>24</thw:PalletCount>", "<thw:PalletCount>0</thw:PalletCount>", 1)
		req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/inbound/orders/soap", strings.NewReader(invalidXML))
		req.Header.Set("Content-Type", "text/xml")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("expected HTTP 400 Bad Request, got %d", resp.StatusCode)
		}

		body, _ := io.ReadAll(resp.Body)
		bodyStr := string(body)
		if !strings.Contains(bodyStr, "<faultstring>") {
			t.Errorf("expected SOAP Fault, got:\n%s", bodyStr)
		}
	})

	// 7. Verify Ledger Audit Log
	t.Run("Ledger audit log endpoint returns events", func(t *testing.T) {
		resp, err := http.Get(ts.URL + "/api/v1/ledger/events")
		if err != nil {
			t.Fatalf("ledger request failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("expected 200, got %d", resp.StatusCode)
		}

		var events []ledger.Event
		if err := json.NewDecoder(resp.Body).Decode(&events); err != nil {
			t.Fatalf("failed decoding ledger json: %v", err)
		}

		if len(events) != 2 {
			t.Fatalf("expected 2 events in ledger (1 success + 1 fail), got %d", len(events))
		}

		// Most recent event first: the failed one
		if events[0].Status != "FAILED" {
			t.Errorf("expected first event to be FAILED, got %s", events[0].Status)
		}
		// Second event: the processed one
		if events[1].Status != "PROCESSED" {
			t.Errorf("expected second event to be PROCESSED, got %s", events[1].Status)
		}
		if events[1].CarrierTripCode != "CARRIER-TRIP-9988" {
			t.Errorf("expected CarrierTripCode CARRIER-TRIP-9988, got %s", events[1].CarrierTripCode)
		}
	})
}
