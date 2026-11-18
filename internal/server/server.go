package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/goktasenes/tahwell/internal/adapter/rest"
	"github.com/goktasenes/tahwell/internal/adapter/soap"
	"github.com/goktasenes/tahwell/internal/ledger"
	"github.com/goktasenes/tahwell/pkg/canonical"
)

// CarrierService interface allows mocking the Carrier adapter in tests.
type CarrierService interface {
	CreateShipment(ctx context.Context, order *canonical.TransportOrder) (*rest.ShipmentCreateResponse, error)
}

// Config holds runtime server settings.
type Config struct {
	Port           int
	CarrierBaseURL string
	CarrierToken   string
	DefaultShipper string
	DefaultCarrier string
	PlantLookups   map[string]string
}

// Server is the HTTP server for Tahwell integration gateway.
type Server struct {
	mux            *http.ServeMux
	ledger         *ledger.Ledger
	carrierClient  *rest.CarrierClient
	defaultShipper string
	defaultCarrier string
	plantLookups   map[string]string
}

// NewServer builds and configures a new Server instance.
func NewServer(l *ledger.Ledger, carrierClient *rest.CarrierClient, defaultShipper, defaultCarrier string, plantLookups map[string]string) *Server {
	if defaultShipper == "" {
		defaultShipper = "Enterprise Shipper"
	}
	if defaultCarrier == "" {
		defaultCarrier = "Carrier Logistics"
	}

	s := &Server{
		mux:            http.NewServeMux(),
		ledger:         l,
		carrierClient:  carrierClient,
		defaultShipper: defaultShipper,
		defaultCarrier: defaultCarrier,
		plantLookups:   plantLookups,
	}
	s.routes()
	return s
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /health", s.handleHealth)
	s.mux.HandleFunc("GET /api/v1/ledger/events", s.handleListEvents)
	s.mux.HandleFunc("POST /api/v1/inbound/orders/soap", s.handleSOAPInbound)
	s.mux.HandleFunc("POST /api/v1/inbound/orders", s.handleSOAPInbound)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"status":  "ok",
		"app":     "Tahwell",
		"version": "1.0.0",
	})
}

func (s *Server) handleListEvents(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if s.ledger == nil {
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode([]any{})
		return
	}

	events, err := s.ledger.ListRecent(r.Context(), 50)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error": %q}`, err.Error()), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(events)
}

func (s *Server) handleSOAPInbound(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	eventID := "evt-" + uuid.NewString()[:8]

	rawXML, err := io.ReadAll(r.Body)
	if err != nil {
		s.respondSOAPFault(w, "Failed to read request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	// 1. Parse Inbound SOAP Request
	req, err := soap.ParseSOAPRequest(rawXML)
	if err != nil {
		s.recordAudit(r.Context(), eventID, s.defaultShipper, s.defaultCarrier, "UNKNOWN", string(rawXML), "", "FAILED", "", err.Error(), time.Since(start))
		s.respondSOAPFault(w, fmt.Sprintf("SOAP parsing error: %v", err), http.StatusBadRequest)
		return
	}

	// 2. Convert to Canonical Transport Order
	order, err := req.ToCanonical(s.defaultShipper, s.plantLookups)
	if err != nil {
		s.recordAudit(r.Context(), eventID, s.defaultShipper, s.defaultCarrier, req.OrderNumber, string(rawXML), "", "FAILED", "", err.Error(), time.Since(start))
		s.respondSOAPFault(w, fmt.Sprintf("Data validation error: %v", err), http.StatusBadRequest)
		return
	}

	// 3. Dispatch to Target Carrier REST API
	transformedJSON, _ := json.Marshal(rest.FromCanonical(order))
	carrierResp, err := s.carrierClient.CreateShipment(r.Context(), order)
	if err != nil {
		s.recordAudit(r.Context(), eventID, order.ShipperName, s.defaultCarrier, order.ReferenceID, string(rawXML), string(transformedJSON), "FAILED", "", err.Error(), time.Since(start))
		s.respondSOAPFault(w, fmt.Sprintf("Carrier integration error: %v", err), http.StatusBadGateway)
		return
	}

	// 4. Record Success in Ledger
	s.recordAudit(r.Context(), eventID, order.ShipperName, s.defaultCarrier, order.ReferenceID, string(rawXML), string(transformedJSON), "PROCESSED", carrierResp.TripID, "", time.Since(start))

	// 5. Build and return SOAP Response
	soapResp, err := soap.BuildSOAPResponse(order.ReferenceID, carrierResp.TripID, "Shipment dispatched successfully via Tahwell Gateway")
	if err != nil {
		s.respondSOAPFault(w, "Internal error formatting SOAP response", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/xml; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(soapResp)
}

func (s *Server) respondSOAPFault(w http.ResponseWriter, message string, statusCode int) {
	faultBytes, err := soap.BuildSOAPFault(message)
	if err != nil {
		http.Error(w, message, statusCode)
		return
	}
	w.Header().Set("Content-Type", "text/xml; charset=utf-8")
	w.WriteHeader(statusCode)
	_, _ = w.Write(faultBytes)
}

func (s *Server) recordAudit(ctx context.Context, eventID, shipper, carrier, refID, rawPayload, transformedPayload, status, tripCode, errMsg string, duration time.Duration) {
	if s.ledger == nil {
		return
	}
	event := &ledger.Event{
		EventID:            eventID,
		Shipper:            shipper,
		Carrier:            carrier,
		ReferenceID:        refID,
		Direction:          "INBOUND",
		SourceFormat:       "SOAP_XML",
		TargetFormat:       "REST_JSON",
		RawPayload:         rawPayload,
		TransformedPayload: transformedPayload,
		Status:             status,
		CarrierTripCode:    tripCode,
		ErrorMessage:       errMsg,
		DurationMs:         duration.Milliseconds(),
		CreatedAt:          time.Now().UTC(),
	}
	if err := s.ledger.Record(ctx, event); err != nil {
		log.Printf("[LEDGER ERROR] Failed to record event %s: %v", eventID, err)
	}
}
