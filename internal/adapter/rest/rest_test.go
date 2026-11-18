package rest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/goktasenes/tahwell/pkg/canonical"
)

func TestCarrierClient_CreateShipment(t *testing.T) {
	testOrder := &canonical.TransportOrder{
		ReferenceID: "ORD-90412",
		ShipperName: "ACME Global",
		Status:      canonical.StatusDraft,
		CreatedAt:   time.Now().UTC(),
		Pickup: canonical.Location{
			FacilityCode: "PLANT-01",
			CompanyName:  "ACME Manufacturing Facility",
			Address:      "Industrial Zone 4th Ave No:12",
			City:         "Origin City",
			ScheduledAt:  time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC),
		},
		Delivery: canonical.Location{
			CompanyName: "Distribution Hub North",
			Address:     "Logistics Park Gateway 5",
			City:        "Metropolis",
			ScheduledAt: time.Date(2026, 11, 21, 9, 0, 0, 0, time.UTC),
		},
		Cargo: canonical.CargoDetails{
			PackageType:   canonical.PackagePallet,
			Quantity:      24,
			GrossWeightKg: 18500.0,
			Notes:         "Handle with care",
		},
		Vehicle: canonical.VehicleReqs{
			VehicleType: "CURTAINSIDER",
		},
	}

	t.Run("Successful shipment creation", func(t *testing.T) {
		mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				t.Errorf("expected POST, got %s", r.Method)
			}
			if r.URL.Path != "/api/v1/shipments" {
				t.Errorf("expected /api/v1/shipments, got %s", r.URL.Path)
			}
			if r.Header.Get("Authorization") != "Bearer secret_token_123" {
				t.Errorf("missing or invalid auth header: %s", r.Header.Get("Authorization"))
			}

			var req ShipmentCreateRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("failed decoding request body: %v", err)
			}

			if req.ShipperReference != "ORD-90412" {
				t.Errorf("expected ORD-90412, got %s", req.ShipperReference)
			}
			if req.Cargo.Quantity != 24 {
				t.Errorf("expected 24, got %d", req.Cargo.Quantity)
			}

			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(ShipmentCreateResponse{
				Status:         "confirmed",
				TripID:         "TRIP-88419",
				EstimatedPrice: 28500.00,
				CreatedAt:      time.Now().UTC().Format(time.RFC3339),
			})
		}))
		defer mockServer.Close()

		client := NewCarrierClient(mockServer.URL, "secret_token_123", nil)
		resp, err := client.CreateShipment(context.Background(), testOrder)
		if err != nil {
			t.Fatalf("expected success, got error: %v", err)
		}

		if resp.TripID != "TRIP-88419" {
			t.Errorf("expected TRIP-88419, got %s", resp.TripID)
		}
		if resp.Status != "confirmed" {
			t.Errorf("expected confirmed, got %s", resp.Status)
		}
	})

	t.Run("Carrier API Error handling", func(t *testing.T) {
		mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(`{"error":"delivery address is outside coverage"}`))
		}))
		defer mockServer.Close()

		client := NewCarrierClient(mockServer.URL, "token", nil)
		_, err := client.CreateShipment(context.Background(), testOrder)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})
}
