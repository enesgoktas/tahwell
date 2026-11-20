package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/goktasenes/tahwell/internal/adapter/rest"
	"github.com/goktasenes/tahwell/internal/adapter/soap"
	"github.com/goktasenes/tahwell/internal/ledger"
	"github.com/goktasenes/tahwell/internal/outbox"
	"github.com/goktasenes/tahwell/pkg/canonical"
	"github.com/goktasenes/tahwell/pkg/mapping"
	"github.com/goktasenes/tahwell/pkg/profile"
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
	engine         *mapping.Engine
	profiles       *profile.Registry
	outboxStore    *outbox.Store
	outboxWorker   *outbox.Worker
}

// NewServer builds and configures a new Server instance with ledger and outbox support.
func NewServer(l *ledger.Ledger, carrierClient *rest.CarrierClient, defaultShipper, defaultCarrier string, plantLookups map[string]string) *Server {
	if defaultShipper == "" {
		defaultShipper = "Enterprise Shipper"
	}
	if defaultCarrier == "" {
		defaultCarrier = "Carrier Logistics"
	}

	var outboxStore *outbox.Store
	var outboxWorker *outbox.Worker
	if l != nil && l.DB() != nil {
		var err error
		outboxStore, err = outbox.NewStore(l.DB())
		if err != nil {
			log.Printf("[OUTBOX WARN] Failed initializing outbox store: %v", err)
		} else if carrierClient != nil {
			outboxWorker = outbox.NewWorker(outboxStore, l, carrierClient, nil)
		}
	}

	s := &Server{
		mux:            http.NewServeMux(),
		ledger:         l,
		carrierClient:  carrierClient,
		defaultShipper: defaultShipper,
		defaultCarrier: defaultCarrier,
		plantLookups:   plantLookups,
		engine:         mapping.NewEngine(),
		profiles:       profile.NewRegistry(),
		outboxStore:    outboxStore,
		outboxWorker:   outboxWorker,
	}
	s.routes()
	return s
}

// SetProfileRegistry sets the active integration profile registry.
func (s *Server) SetProfileRegistry(reg *profile.Registry) {
	if reg != nil {
		s.profiles = reg
	}
}

// ProfileRegistry returns the active integration profile registry.
func (s *Server) ProfileRegistry() *profile.Registry {
	return s.profiles
}

// OutboxStore returns the configured outbox store.
func (s *Server) OutboxStore() *outbox.Store {
	return s.outboxStore
}

// OutboxWorker returns the background outbox retry worker.
func (s *Server) OutboxWorker() *outbox.Worker {
	return s.outboxWorker
}

// StartOutbox starts the background retry worker if available.
func (s *Server) StartOutbox(ctx context.Context) {
	if s.outboxWorker != nil {
		s.outboxWorker.Start(ctx)
	}
}

// StopOutbox halts the background retry worker.
func (s *Server) StopOutbox() {
	if s.outboxWorker != nil {
		s.outboxWorker.Stop()
	}
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /health", s.handleHealth)
	s.mux.HandleFunc("GET /api/v1/ledger/events", s.handleListEvents)
	s.mux.HandleFunc("GET /api/v1/outbox/tasks", s.handleListOutboxTasks)
	s.mux.HandleFunc("GET /api/v1/outbox/stats", s.handleOutboxStats)
	s.mux.HandleFunc("POST /api/v1/outbox/tasks/{task_id}/retry", s.handleRetryOutboxTask)
	s.mux.HandleFunc("POST /api/v1/inbound/orders/soap", s.handleSOAPInbound)
	s.mux.HandleFunc("POST /api/v1/inbound/orders", s.handleInboundOrder)
	s.mux.HandleFunc("POST /api/v1/inbound/orders/{profile_id}", s.handleInboundOrderWithProfile)
	s.mux.HandleFunc("POST /api/v1/webhooks/carrier", s.handleCarrierWebhook)
	s.mux.HandleFunc("POST /api/v1/webhooks/carrier/{profile_id}", s.handleCarrierWebhookWithProfile)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"status":  "ok",
		"app":     "Tahwell",
		"version": "1.2.0",
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

func (s *Server) handleListOutboxTasks(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if s.outboxStore == nil {
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode([]any{})
		return
	}
	status := r.URL.Query().Get("status")
	tasks, err := s.outboxStore.List(r.Context(), status, 50)
	if err != nil {
		s.respondJSONError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(tasks)
}

func (s *Server) handleOutboxStats(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if s.outboxStore == nil {
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]int64{})
		return
	}
	stats, err := s.outboxStore.Stats(r.Context())
	if err != nil {
		s.respondJSONError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(stats)
}

func (s *Server) handleRetryOutboxTask(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	taskID := r.PathValue("task_id")
	if s.outboxStore == nil {
		s.respondJSONError(w, "outbox store not configured", http.StatusInternalServerError)
		return
	}
	if err := s.outboxStore.ScheduleRetry(r.Context(), taskID, time.Now().UTC()); err != nil {
		s.respondJSONError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"success": true,
		"task_id": taskID,
		"message": "Task queued for immediate retry",
	})
}

// handleSOAPInbound handles legacy / explicit SOAP/XML inbound requests.
func (s *Server) handleSOAPInbound(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	eventID := "evt-" + uuid.NewString()[:8]

	rawXML, err := io.ReadAll(r.Body)
	if err != nil {
		s.respondSOAPFault(w, "Failed to read request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	s.processSOAPInboundWithBytes(w, r, rawXML, start, eventID)
}

func (s *Server) processSOAPInboundWithBytes(w http.ResponseWriter, r *http.Request, rawXML []byte, start time.Time, eventID string) {
	// 1. Parse Inbound SOAP Request
	req, err := soap.ParseSOAPRequest(rawXML)
	if err != nil {
		s.recordAudit(r.Context(), eventID, s.defaultShipper, s.defaultCarrier, "UNKNOWN", "INBOUND", "SOAP_XML", "REST_JSON", string(rawXML), "", "FAILED", "", err.Error(), time.Since(start))
		s.respondSOAPFault(w, fmt.Sprintf("SOAP parsing error: %v", err), http.StatusBadRequest)
		return
	}

	// 2. Convert to Canonical Transport Order
	order, err := req.ToCanonical(s.defaultShipper, s.plantLookups)
	if err != nil {
		s.recordAudit(r.Context(), eventID, s.defaultShipper, s.defaultCarrier, req.OrderNumber, "INBOUND", "SOAP_XML", "REST_JSON", string(rawXML), "", "FAILED", "", err.Error(), time.Since(start))
		s.respondSOAPFault(w, fmt.Sprintf("Data validation error: %v", err), http.StatusBadRequest)
		return
	}

	// 3. Dispatch to Target Carrier REST API
	transformedJSON, _ := json.Marshal(rest.FromCanonical(order))
	carrierResp, err := s.carrierClient.CreateShipment(r.Context(), order)
	if err != nil {
		// Resilience: fallback to outbox for guaranteed delivery
		if s.outboxStore != nil {
			taskID := "task-" + uuid.NewString()[:8]
			targetURL := s.carrierClient.BaseURL() + "/api/v1/shipments"
			_ = s.outboxStore.Enqueue(r.Context(), &outbox.Task{
				TaskID:      taskID,
				ProfileID:   "default",
				ReferenceID: order.ReferenceID,
				TaskType:    outbox.TypeOrderDispatch,
				TargetURL:   targetURL,
				Payload:     string(transformedJSON),
				LastError:   err.Error(),
			})
			s.recordAudit(r.Context(), eventID, order.ShipperName, s.defaultCarrier, order.ReferenceID, "INBOUND", "SOAP_XML", "REST_JSON", string(rawXML), string(transformedJSON), "QUEUED_FOR_RETRY", "PENDING_RETRY", err.Error(), time.Since(start))
			soapResp, _ := soap.BuildSOAPResponse(order.ReferenceID, "PENDING_RETRY", "Carrier temporarily unavailable. Order queued in outbox for guaranteed delivery")
			w.Header().Set("Content-Type", "text/xml; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(soapResp)
			return
		}

		s.recordAudit(r.Context(), eventID, order.ShipperName, s.defaultCarrier, order.ReferenceID, "INBOUND", "SOAP_XML", "REST_JSON", string(rawXML), string(transformedJSON), "FAILED", "", err.Error(), time.Since(start))
		s.respondSOAPFault(w, fmt.Sprintf("Carrier integration error: %v", err), http.StatusBadGateway)
		return
	}

	// 4. Record Success in Ledger
	s.recordAudit(r.Context(), eventID, order.ShipperName, s.defaultCarrier, order.ReferenceID, "INBOUND", "SOAP_XML", "REST_JSON", string(rawXML), string(transformedJSON), "PROCESSED", carrierResp.TripID, "", time.Since(start))

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

func (s *Server) handleInboundOrderWithProfile(w http.ResponseWriter, r *http.Request) {
	s.processInboundOrder(w, r, r.PathValue("profile_id"))
}

func (s *Server) handleInboundOrder(w http.ResponseWriter, r *http.Request) {
	profileID := r.Header.Get("X-Tahwell-Profile")
	if profileID == "" {
		profileID = r.URL.Query().Get("profile")
	}
	s.processInboundOrder(w, r, profileID)
}

func (s *Server) processInboundOrder(w http.ResponseWriter, r *http.Request, profileID string) {
	start := time.Now()
	eventID := "evt-" + uuid.NewString()[:8]

	rawBytes, err := io.ReadAll(r.Body)
	if err != nil {
		s.respondError(w, r, "Failed to read request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	contentType := r.Header.Get("Content-Type")

	// If no profile provided and payload is XML, maintain backwards compatibility with standard SOAP flow
	if profileID == "" && (strings.Contains(strings.ToLower(contentType), "xml") || strings.HasPrefix(strings.TrimSpace(string(rawBytes)), "<")) {
		s.processSOAPInboundWithBytes(w, r, rawBytes, start, eventID)
		return
	}

	// 1. Ingest via Generic Parser
	rawMap, err := mapping.ParseAuto(contentType, rawBytes)
	if err != nil {
		s.recordAudit(r.Context(), eventID, s.defaultShipper, s.defaultCarrier, "UNKNOWN", "INBOUND", contentType, "CANONICAL", string(rawBytes), "", "FAILED", "", err.Error(), time.Since(start))
		s.respondError(w, r, fmt.Sprintf("Failed parsing payload: %v", err), http.StatusBadRequest)
		return
	}

	// 2. Fetch Profile if specified
	var prof *profile.Profile
	if profileID != "" && s.profiles != nil {
		var found bool
		prof, found = s.profiles.Get(profileID)
		if !found {
			errMsg := fmt.Sprintf("Profile %q not found", profileID)
			s.recordAudit(r.Context(), eventID, s.defaultShipper, s.defaultCarrier, "UNKNOWN", "INBOUND", contentType, "CANONICAL", string(rawBytes), "", "FAILED", "", errMsg, time.Since(start))
			s.respondError(w, r, errMsg, http.StatusNotFound)
			return
		}
	}

	shipper := s.defaultShipper
	carrier := s.defaultCarrier
	if prof != nil {
		if prof.Shipper != "" {
			shipper = prof.Shipper
		}
		if prof.Carrier != "" {
			carrier = prof.Carrier
		}
	}

	// 3. Normalize to Canonical TransportOrder
	var order *canonical.TransportOrder
	if prof != nil && prof.InboundOrderSpec != nil {
		var normMap map[string]any
		normMap, err = s.engine.Transform(prof.InboundOrderSpec, rawMap)
		if err == nil {
			order, err = canonical.OrderFromMap(normMap)
		}
	} else {
		order, err = canonical.OrderFromMap(rawMap)
	}

	if err != nil {
		refID := "UNKNOWN"
		if order != nil && order.ReferenceID != "" {
			refID = order.ReferenceID
		}
		s.recordAudit(r.Context(), eventID, shipper, carrier, refID, "INBOUND", contentType, "CANONICAL", string(rawBytes), "", "FAILED", "", err.Error(), time.Since(start))
		s.respondError(w, r, fmt.Sprintf("Validation / normalization error: %v", err), http.StatusBadRequest)
		return
	}

	if order.ShipperName == "" {
		order.ShipperName = shipper
	}

	// 4. Dispatch to Target Carrier
	var tripID string
	var transformedJSON []byte

	if prof != nil && prof.CarrierEndpointURL != "" {
		var outboundPayload any
		if prof.OutboundOrderSpec != nil {
			orderMap, mapErr := order.ToMap()
			if mapErr != nil {
				s.recordAudit(r.Context(), eventID, shipper, carrier, order.ReferenceID, "INBOUND", contentType, "REST_JSON", string(rawBytes), "", "FAILED", "", mapErr.Error(), time.Since(start))
				s.respondError(w, r, fmt.Sprintf("Order serialization error: %v", mapErr), http.StatusInternalServerError)
				return
			}
			outboundMap, transErr := s.engine.Transform(prof.OutboundOrderSpec, orderMap)
			if transErr != nil {
				s.recordAudit(r.Context(), eventID, shipper, carrier, order.ReferenceID, "INBOUND", contentType, "REST_JSON", string(rawBytes), "", "FAILED", "", transErr.Error(), time.Since(start))
				s.respondError(w, r, fmt.Sprintf("Outbound transformation error: %v", transErr), http.StatusInternalServerError)
				return
			}
			outboundPayload = outboundMap
		} else {
			outboundPayload = rest.FromCanonical(order)
		}

		transformedJSON, _ = json.Marshal(outboundPayload)
		respMap, dispatchErr := s.carrierClient.DispatchGeneric(r.Context(), prof.CarrierEndpointURL, prof.CarrierAuthToken, outboundPayload)
		if dispatchErr != nil {
			// Resilience: Enqueue into Outbox
			if s.outboxStore != nil {
				taskID := "task-" + uuid.NewString()[:8]
				_ = s.outboxStore.Enqueue(r.Context(), &outbox.Task{
					TaskID:      taskID,
					ProfileID:   profileID,
					ReferenceID: order.ReferenceID,
					TaskType:    outbox.TypeOrderDispatch,
					TargetURL:   prof.CarrierEndpointURL,
					AuthToken:   prof.CarrierAuthToken,
					Payload:     string(transformedJSON),
					LastError:   dispatchErr.Error(),
				})
				s.recordAudit(r.Context(), eventID, shipper, carrier, order.ReferenceID, "INBOUND", contentType, "REST_JSON", string(rawBytes), string(transformedJSON), "QUEUED_FOR_RETRY", "PENDING_RETRY", dispatchErr.Error(), time.Since(start))
				s.respondQueuedOrder(w, r, contentType, order.ReferenceID, taskID)
				return
			}

			s.recordAudit(r.Context(), eventID, shipper, carrier, order.ReferenceID, "INBOUND", contentType, "REST_JSON", string(rawBytes), string(transformedJSON), "FAILED", "", dispatchErr.Error(), time.Since(start))
			s.respondError(w, r, fmt.Sprintf("Carrier dispatch error: %v", dispatchErr), http.StatusBadGateway)
			return
		}

		if t, ok := respMap["trip_id"].(string); ok {
			tripID = t
		} else if t, ok := respMap["carrier_trip_code"].(string); ok {
			tripID = t
		} else {
			tripID = "CARRIER-TRIP-" + uuid.NewString()[:8]
		}
	} else {
		transformedJSON, _ = json.Marshal(rest.FromCanonical(order))
		carrierResp, carrierErr := s.carrierClient.CreateShipment(r.Context(), order)
		if carrierErr != nil {
			if s.outboxStore != nil {
				taskID := "task-" + uuid.NewString()[:8]
				targetURL := s.carrierClient.BaseURL() + "/api/v1/shipments"
				_ = s.outboxStore.Enqueue(r.Context(), &outbox.Task{
					TaskID:      taskID,
					ProfileID:   profileID,
					ReferenceID: order.ReferenceID,
					TaskType:    outbox.TypeOrderDispatch,
					TargetURL:   targetURL,
					Payload:     string(transformedJSON),
					LastError:   carrierErr.Error(),
				})
				s.recordAudit(r.Context(), eventID, shipper, carrier, order.ReferenceID, "INBOUND", contentType, "REST_JSON", string(rawBytes), string(transformedJSON), "QUEUED_FOR_RETRY", "PENDING_RETRY", carrierErr.Error(), time.Since(start))
				s.respondQueuedOrder(w, r, contentType, order.ReferenceID, taskID)
				return
			}

			s.recordAudit(r.Context(), eventID, shipper, carrier, order.ReferenceID, "INBOUND", contentType, "REST_JSON", string(rawBytes), string(transformedJSON), "FAILED", "", carrierErr.Error(), time.Since(start))
			s.respondError(w, r, fmt.Sprintf("Carrier integration error: %v", carrierErr), http.StatusBadGateway)
			return
		}
		tripID = carrierResp.TripID
	}

	// 5. Record Success in Ledger
	s.recordAudit(r.Context(), eventID, shipper, carrier, order.ReferenceID, "INBOUND", contentType, "REST_JSON", string(rawBytes), string(transformedJSON), "PROCESSED", tripID, "", time.Since(start))

	// 6. Respond in appropriate format
	if strings.Contains(strings.ToLower(contentType), "xml") {
		soapResp, _ := soap.BuildSOAPResponse(order.ReferenceID, tripID, "Shipment dispatched successfully via Tahwell Gateway")
		w.Header().Set("Content-Type", "text/xml; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(soapResp)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"success":           true,
		"order_number":      order.ReferenceID,
		"carrier_trip_code": tripID,
		"message":           "Shipment dispatched successfully via Tahwell Gateway",
	})
}

// handleCarrierWebhookWithProfile handles status webhooks from carriers with a specific profile.
func (s *Server) handleCarrierWebhookWithProfile(w http.ResponseWriter, r *http.Request) {
	s.processCarrierWebhook(w, r, r.PathValue("profile_id"))
}

// handleCarrierWebhook handles status webhooks with optional profile header/query.
func (s *Server) handleCarrierWebhook(w http.ResponseWriter, r *http.Request) {
	profileID := r.Header.Get("X-Tahwell-Profile")
	if profileID == "" {
		profileID = r.URL.Query().Get("profile")
	}
	s.processCarrierWebhook(w, r, profileID)
}

func (s *Server) processCarrierWebhook(w http.ResponseWriter, r *http.Request, profileID string) {
	start := time.Now()
	eventID := "evt-stat-" + uuid.NewString()[:8]

	rawBytes, err := io.ReadAll(r.Body)
	if err != nil {
		s.respondJSONError(w, "Failed to read request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	contentType := r.Header.Get("Content-Type")

	// 1. Ingest via Generic Parser
	statusMap, err := mapping.ParseAuto(contentType, rawBytes)
	if err != nil {
		s.recordAudit(r.Context(), eventID, s.defaultShipper, s.defaultCarrier, "UNKNOWN", "INBOUND_STATUS", contentType, "CANONICAL", string(rawBytes), "", "FAILED", "", err.Error(), time.Since(start))
		s.respondJSONError(w, fmt.Sprintf("Failed parsing status payload: %v", err), http.StatusBadRequest)
		return
	}

	// 2. Fetch Profile if specified
	var prof *profile.Profile
	if profileID != "" && s.profiles != nil {
		var found bool
		prof, found = s.profiles.Get(profileID)
		if !found {
			errMsg := fmt.Sprintf("Profile %q not found", profileID)
			s.recordAudit(r.Context(), eventID, s.defaultShipper, s.defaultCarrier, "UNKNOWN", "INBOUND_STATUS", contentType, "CANONICAL", string(rawBytes), "", "FAILED", "", errMsg, time.Since(start))
			s.respondJSONError(w, errMsg, http.StatusNotFound)
			return
		}
	}

	shipper := s.defaultShipper
	carrier := s.defaultCarrier
	if prof != nil {
		if prof.Shipper != "" {
			shipper = prof.Shipper
		}
		if prof.Carrier != "" {
			carrier = prof.Carrier
		}
	}

	// 3. Normalize to Canonical StatusEvent
	var statusEvent *canonical.StatusEvent
	if prof != nil && prof.InboundStatusSpec != nil {
		var normMap map[string]any
		normMap, err = s.engine.Transform(prof.InboundStatusSpec, statusMap)
		if err == nil {
			statusEvent, err = canonical.StatusEventFromMap(normMap)
		}
	} else {
		statusEvent, err = canonical.StatusEventFromMap(statusMap)
	}

	if err != nil {
		refID := "UNKNOWN"
		if statusEvent != nil && statusEvent.OrderReferenceID != "" {
			refID = statusEvent.OrderReferenceID
		}
		s.recordAudit(r.Context(), eventID, shipper, carrier, refID, "INBOUND_STATUS", contentType, "CANONICAL", string(rawBytes), "", "FAILED", "", err.Error(), time.Since(start))
		s.respondJSONError(w, fmt.Sprintf("Status validation error: %v", err), http.StatusBadRequest)
		return
	}

	if statusEvent.EventID == "" {
		statusEvent.EventID = eventID
	}
	if statusEvent.Timestamp.IsZero() {
		statusEvent.Timestamp = time.Now().UTC()
	}

	transformedBytes, _ := json.Marshal(statusEvent)

	// 4. Record Status Event in Ledger
	refID := statusEvent.OrderReferenceID
	if refID == "" {
		refID = statusEvent.CarrierTripCode
	}
	s.recordAudit(r.Context(), eventID, shipper, carrier, refID, "INBOUND_STATUS", contentType, "CANONICAL", string(rawBytes), string(transformedBytes), string(statusEvent.Status), statusEvent.CarrierTripCode, "", time.Since(start))

	// 5. Dispatch ERP Callback if configured
	if prof != nil && prof.ERPCallbackURL != "" {
		var erpPayload any
		if prof.OutboundStatusSpec != nil {
			eventMap, _ := statusEvent.ToMap()
			erpMap, mapErr := s.engine.Transform(prof.OutboundStatusSpec, eventMap)
			if mapErr == nil {
				erpPayload = erpMap
			} else {
				erpPayload = statusEvent
			}
		} else {
			erpPayload = statusEvent
		}

		callbackBytes, _ := json.Marshal(erpPayload)
		cbStart := time.Now()
		cbEventID := "evt-cb-" + uuid.NewString()[:8]

		_, cbErr := s.carrierClient.DispatchGeneric(r.Context(), prof.ERPCallbackURL, "", erpPayload)
		if cbErr != nil {
			// Resilience: fallback to outbox for ERP callback
			if s.outboxStore != nil {
				cbTaskID := "task-cb-" + uuid.NewString()[:8]
				_ = s.outboxStore.Enqueue(r.Context(), &outbox.Task{
					TaskID:      cbTaskID,
					ProfileID:   profileID,
					ReferenceID: refID,
					TaskType:    outbox.TypeERPCallback,
					TargetURL:   prof.ERPCallbackURL,
					Payload:     string(callbackBytes),
					LastError:   cbErr.Error(),
				})
				s.recordAudit(r.Context(), cbEventID, shipper, carrier, refID, "OUTBOUND_CALLBACK", "CANONICAL", "JSON", string(callbackBytes), "", "QUEUED_FOR_RETRY", statusEvent.CarrierTripCode, cbErr.Error(), time.Since(cbStart))
			} else {
				s.recordAudit(r.Context(), cbEventID, shipper, carrier, refID, "OUTBOUND_CALLBACK", "CANONICAL", "JSON", string(callbackBytes), "", "FAILED", statusEvent.CarrierTripCode, cbErr.Error(), time.Since(cbStart))
			}
		} else {
			s.recordAudit(r.Context(), cbEventID, shipper, carrier, refID, "OUTBOUND_CALLBACK", "CANONICAL", "JSON", string(callbackBytes), "", "DELIVERED_TO_ERP", statusEvent.CarrierTripCode, "", time.Since(cbStart))
		}
	}

	// 6. Return response to carrier
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"success":            true,
		"event_id":           statusEvent.EventID,
		"status":             statusEvent.Status,
		"order_reference_id": statusEvent.OrderReferenceID,
		"carrier_trip_code":  statusEvent.CarrierTripCode,
		"message":            "Status update acknowledged by Tahwell Gateway",
	})
}

func (s *Server) respondQueuedOrder(w http.ResponseWriter, r *http.Request, contentType, refID, taskID string) {
	if strings.Contains(strings.ToLower(contentType), "xml") {
		soapResp, _ := soap.BuildSOAPResponse(refID, "PENDING_RETRY", "Carrier temporarily unavailable. Order queued in outbox for guaranteed delivery")
		w.Header().Set("Content-Type", "text/xml; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(soapResp)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"success":           true,
		"status":            "QUEUED_FOR_RETRY",
		"order_number":      refID,
		"carrier_trip_code": "PENDING_RETRY",
		"task_id":           taskID,
		"message":           "Carrier temporarily unavailable. Order enqueued in outbox for automatic delivery.",
	})
}

func (s *Server) respondError(w http.ResponseWriter, r *http.Request, message string, statusCode int) {
	contentType := r.Header.Get("Content-Type")
	if strings.Contains(strings.ToLower(contentType), "xml") {
		s.respondSOAPFault(w, message, statusCode)
		return
	}
	s.respondJSONError(w, message, statusCode)
}

func (s *Server) respondJSONError(w http.ResponseWriter, message string, statusCode int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"success": false,
		"error":   message,
	})
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

func (s *Server) recordAudit(ctx context.Context, eventID, shipper, carrier, refID, direction, sourceFormat, targetFormat, rawPayload, transformedPayload, status, tripCode, errMsg string, duration time.Duration) {
	if s.ledger == nil {
		return
	}
	event := &ledger.Event{
		EventID:            eventID,
		Shipper:            shipper,
		Carrier:            carrier,
		ReferenceID:        refID,
		Direction:          direction,
		SourceFormat:       sourceFormat,
		TargetFormat:       targetFormat,
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
