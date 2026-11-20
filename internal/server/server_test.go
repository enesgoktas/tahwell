package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/goktasenes/tahwell/internal/adapter/rest"
	"github.com/goktasenes/tahwell/internal/ledger"
	"github.com/goktasenes/tahwell/pkg/mapping"
	"github.com/goktasenes/tahwell/pkg/profile"
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
	var lastCarrierPayload map[string]any
	var carrierMu sync.Mutex

	// 1. Mock Target Carrier REST Backend
	mockCarrier := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		carrierMu.Lock()
		defer carrierMu.Unlock()

		_ = json.NewDecoder(r.Body).Decode(&lastCarrierPayload)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":          "confirmed",
			"trip_id":         "CARRIER-TRIP-9988",
			"estimated_price": 28500.0,
			"created_at":      "2026-11-18T16:00:00Z",
		})
	}))
	defer mockCarrier.Close()

	// 2. Mock ERP Callback Server
	var erpReceivedPayload map[string]any
	var erpMu sync.Mutex

	mockERP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		erpMu.Lock()
		defer erpMu.Unlock()

		_ = json.NewDecoder(r.Body).Decode(&erpReceivedPayload)
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{"acknowledged": true})
	}))
	defer mockERP.Close()

	// 3. Setup Ledger
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "server_test.db")
	l, err := ledger.New(dbPath)
	if err != nil {
		t.Fatalf("failed initializing ledger: %v", err)
	}
	defer l.Close()

	// 4. Setup Tahwell Server with Profiles
	plantLookups := map[string]string{
		"PLANT-01": "Central Manufacturing Facility, Plant 1",
	}
	carrierClient := rest.NewCarrierClient(mockCarrier.URL, "test_token", nil)
	srv := NewServer(l, carrierClient, "ACME Shipper", "Carrier Express", plantLookups)

	// Configure Dynamic Profile
	inboundOrderSpec, _ := mapping.LoadSpecFromYAML([]byte(`
name: "custom_json_order"
version: "1.0.0"
rules:
  - source: "po_number"
    target: "reference_id"
    required: true
  - source: "origin.address"
    target: "pickup.address"
    required: true
  - source: "origin.time"
    target: "pickup.scheduled_at"
    transform: "date_iso8601"
  - source: "destination.client"
    target: "delivery.company_name"
    required: true
  - source: "destination.address"
    target: "delivery.address"
    required: true
  - source: "items.pallets"
    target: "cargo.quantity"
    transform: "to_int"
    required: true
  - source: "items.weight_ton"
    target: "cargo.gross_weight_kg"
    transform: "multiply:1000"
    required: true
`))

	outboundOrderSpec, _ := mapping.LoadSpecFromYAML([]byte(`
name: "canonical_to_carrier_custom"
version: "1.0.0"
rules:
  - source: "reference_id"
    target: "order_code"
  - source: "cargo.gross_weight_kg"
    target: "total_kg"
`))

	inboundStatusSpec, _ := mapping.LoadSpecFromYAML([]byte(`
name: "carrier_webhook_spec"
version: "1.0.0"
rules:
  - source: "carrier_ref"
    target: "carrier_trip_code"
    required: true
  - source: "order_id"
    target: "order_reference_id"
  - source: "event_type"
    target: "status"
    required: true
  - source: "event_time"
    target: "timestamp"
    transform: "date_iso8601"
  - source: "proof.document_url"
    target: "pod_url"
`))

	outboundStatusSpec, _ := mapping.LoadSpecFromYAML([]byte(`
name: "status_to_erp_spec"
version: "1.0.0"
rules:
  - source: "order_reference_id"
    target: "sap_po_number"
  - source: "status"
    target: "delivery_state"
  - source: "pod_url"
    target: "pod_attachment"
`))

	testProfile := &profile.Profile{
		ID:                 "profile-alpha",
		Name:               "Alpha Shipper to Carrier X",
		Shipper:            "Alpha Manufacturer",
		Carrier:            "Carrier X",
		CarrierEndpointURL: mockCarrier.URL + "/api/v1/shipments",
		CarrierAuthToken:   "bearer_secret_xyz",
		ERPCallbackURL:     mockERP.URL + "/api/v1/callbacks",
		InboundOrderSpec:   inboundOrderSpec,
		OutboundOrderSpec:  outboundOrderSpec,
		InboundStatusSpec:  inboundStatusSpec,
		OutboundStatusSpec: outboundStatusSpec,
	}
	srv.ProfileRegistry().Register(testProfile)

	ts := httptest.NewServer(srv)
	defer ts.Close()

	// 5. Test Health Check
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

	// 6. Test Legacy Inbound SOAP Request (Backwards Compatibility)
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

	// 7. Test Format-Agnostic JSON Inbound Order via Profile
	t.Run("Format-Agnostic JSON Inbound Order via Profile", func(t *testing.T) {
		jsonOrder := `{
			"po_number": "PO-88192",
			"origin": {
				"address": "Factory Street No:42 Bursa",
				"time": "20.11.2026 08:30"
			},
			"destination": {
				"client": "Metro Hypermarket",
				"address": "Distribution Blvd No:100 Istanbul"
			},
			"items": {
				"pallets": 18,
				"weight_ton": 14.5
			}
		}`

		req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/inbound/orders/profile-alpha", strings.NewReader(jsonOrder))
		if err != nil {
			t.Fatalf("building request failed: %v", err)
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("expected HTTP 200, got %d: %s", resp.StatusCode, string(body))
		}

		var respData map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&respData); err != nil {
			t.Fatalf("decoding json response: %v", err)
		}

		if respData["success"] != true {
			t.Errorf("expected success true, got %v", respData["success"])
		}
		if respData["order_number"] != "PO-88192" {
			t.Errorf("expected order_number PO-88192, got %v", respData["order_number"])
		}

		// Verify what the mock carrier received
		carrierMu.Lock()
		defer carrierMu.Unlock()
		if lastCarrierPayload["order_code"] != "PO-88192" {
			t.Errorf("expected carrier order_code PO-88192, got %v", lastCarrierPayload["order_code"])
		}
		if lastCarrierPayload["total_kg"] != 14500.0 {
			t.Errorf("expected carrier total_kg 14500, got %v", lastCarrierPayload["total_kg"])
		}
	})

	// 8. Test Inbound Carrier Status Webhook with ERP Callback
	t.Run("Inbound Carrier Status Webhook with ERP Callback", func(t *testing.T) {
		carrierWebhookJSON := `{
			"carrier_ref": "CARRIER-TRIP-9988",
			"order_id": "PO-88192",
			"event_type": "DELIVERED",
			"event_time": "20.11.2026 15:45:00",
			"proof": {
				"document_url": "https://storage.carrierx.com/pod/receipt_9988.pdf"
			}
		}`

		req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/webhooks/carrier/profile-alpha", strings.NewReader(carrierWebhookJSON))
		if err != nil {
			t.Fatalf("building request failed: %v", err)
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("expected HTTP 200, got %d: %s", resp.StatusCode, string(body))
		}

		var ackData map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&ackData)
		if ackData["success"] != true {
			t.Errorf("expected success true, got %v", ackData["success"])
		}
		if ackData["status"] != "DELIVERED" {
			t.Errorf("expected DELIVERED, got %v", ackData["status"])
		}

		// Verify ERP received the callback with mapped fields
		erpMu.Lock()
		defer erpMu.Unlock()
		if erpReceivedPayload["sap_po_number"] != "PO-88192" {
			t.Errorf("expected ERP sap_po_number PO-88192, got %v", erpReceivedPayload["sap_po_number"])
		}
		if erpReceivedPayload["delivery_state"] != "DELIVERED" {
			t.Errorf("expected ERP delivery_state DELIVERED, got %v", erpReceivedPayload["delivery_state"])
		}
		if erpReceivedPayload["pod_attachment"] != "https://storage.carrierx.com/pod/receipt_9988.pdf" {
			t.Errorf("expected ERP pod_attachment, got %v", erpReceivedPayload["pod_attachment"])
		}
	})

	// 9. Verify Ledger Records All Lifecycle Events
	t.Run("Ledger audit log records multi-format and webhook events", func(t *testing.T) {
		resp, err := http.Get(ts.URL + "/api/v1/ledger/events")
		if err != nil {
			t.Fatalf("ledger request failed: %v", err)
		}
		defer resp.Body.Close()

		var events []ledger.Event
		if err := json.NewDecoder(resp.Body).Decode(&events); err != nil {
			t.Fatalf("failed decoding ledger json: %v", err)
		}

		// We should have:
		// 1. SOAP Inbound Order (PROCESSED)
		// 2. Profile-Alpha Inbound Order (PROCESSED)
		// 3. Status Webhook (DELIVERED)
		// 4. ERP Callback Dispatch (DELIVERED_TO_ERP)
		if len(events) < 4 {
			t.Fatalf("expected at least 4 ledger events, got %d", len(events))
		}

		// Check presence of INBOUND_STATUS and OUTBOUND_CALLBACK directions
		hasStatusInbound := false
		hasERPCallback := false

		for _, e := range events {
			if e.Direction == "INBOUND_STATUS" && e.ReferenceID == "PO-88192" {
				hasStatusInbound = true
			}
			if e.Direction == "OUTBOUND_CALLBACK" && e.ReferenceID == "PO-88192" {
				hasERPCallback = true
			}
		}

		if !hasStatusInbound {
			t.Errorf("expected to find INBOUND_STATUS event in ledger for PO-88192")
		}
		if !hasERPCallback {
			t.Errorf("expected to find OUTBOUND_CALLBACK event in ledger for PO-88192")
		}
	})

	// 10. Test Outbox Fallback when Carrier is Down
	t.Run("Outbox Fallback and Recovery on Carrier Failure", func(t *testing.T) {
		// Temporary broken carrier server returning 503
		brokenCarrier := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":"Service temporarily unavailable"}`))
		}))
		defer brokenCarrier.Close()

		brokenProfile := &profile.Profile{
			ID:                 "broken-carrier-profile",
			Name:               "Broken Carrier Profile",
			CarrierEndpointURL: brokenCarrier.URL,
			InboundOrderSpec:   inboundOrderSpec,
			OutboundOrderSpec:  outboundOrderSpec,
		}
		srv.ProfileRegistry().Register(brokenProfile)

		jsonOrder := `{
			"po_number": "PO-RETRY-777",
			"origin": {"address": "Plant A", "time": "20.11.2026 09:00"},
			"destination": {"client": "Client B", "address": "Address B"},
			"items": {"pallets": 5, "weight_ton": 3.0}
		}`

		req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/inbound/orders/broken-carrier-profile", strings.NewReader(jsonOrder))
		req.Header.Set("Content-Type", "application/json")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusAccepted {
			t.Fatalf("expected HTTP 202 Accepted, got %d", resp.StatusCode)
		}

		var ack map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&ack)
		if ack["status"] != "QUEUED_FOR_RETRY" {
			t.Errorf("expected QUEUED_FOR_RETRY, got %v", ack["status"])
		}

		// Verify task is in Outbox
		statsResp, _ := http.Get(ts.URL + "/api/v1/outbox/stats")
		var stats map[string]int64
		_ = json.NewDecoder(statsResp.Body).Decode(&stats)
		statsResp.Body.Close()

		if stats["PENDING"] < 1 {
			t.Errorf("expected at least 1 pending task in outbox, got %d", stats["PENDING"])
		}
	})
}
