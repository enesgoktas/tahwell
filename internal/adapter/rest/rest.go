package rest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/goktasenes/tahwell/pkg/canonical"
)

// Generic Carrier REST JSON request/response types.
type LocationPayload struct {
	FacilityCode string `json:"facility_code,omitempty"`
	CompanyName  string `json:"company_name"`
	Address      string `json:"address"`
	City         string `json:"city"`
	ScheduledAt  string `json:"scheduled_at"`
}

type CargoPayload struct {
	PackageType   string  `json:"package_type"`
	Quantity      int     `json:"quantity"`
	GrossWeightKg float64 `json:"weight_kg"`
	Notes         string  `json:"notes,omitempty"`
}

type ShipmentCreateRequest struct {
	ShipperReference string          `json:"shipper_reference"`
	VehicleType      string          `json:"vehicle_type,omitempty"`
	Pickup           LocationPayload `json:"pickup"`
	Dropoff          LocationPayload `json:"dropoff"`
	Cargo            CargoPayload    `json:"cargo"`
}

type ShipmentCreateResponse struct {
	Status         string  `json:"status"`
	TripID         string  `json:"trip_id"`
	EstimatedPrice float64 `json:"estimated_price,omitempty"`
	CreatedAt      string  `json:"created_at"`
	Message        string  `json:"message,omitempty"`
}

// CarrierClient interacts with a carrier's modern REST API.
type CarrierClient struct {
	baseURL    string
	apiToken   string
	httpClient *http.Client
}

// NewCarrierClient initializes a new generic carrier REST client.
func NewCarrierClient(baseURL, apiToken string, client *http.Client) *CarrierClient {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &CarrierClient{
		baseURL:    strings.TrimRight(baseURL, "/"),
		apiToken:   apiToken,
		httpClient: client,
	}
}

// BaseURL returns the configured base URL for the carrier API.
func (c *CarrierClient) BaseURL() string {
	return c.baseURL
}

// FromCanonical builds a generic ShipmentCreateRequest from a Canonical TransportOrder.
func FromCanonical(order *canonical.TransportOrder) ShipmentCreateRequest {
	vehicleType := "curtainsider_truck"
	upperVehicle := strings.ToUpper(order.Vehicle.VehicleType)
	if strings.Contains(upperVehicle, "RIGID") || strings.Contains(upperVehicle, "KIRKAYAK") {
		vehicleType = "rigid_truck"
	} else if strings.Contains(upperVehicle, "FRIGO") || strings.Contains(upperVehicle, "REEFER") {
		vehicleType = "reefer_truck"
	}

	return ShipmentCreateRequest{
		ShipperReference: order.ReferenceID,
		VehicleType:      vehicleType,
		Pickup: LocationPayload{
			FacilityCode: order.Pickup.FacilityCode,
			CompanyName:  order.Pickup.CompanyName,
			Address:      order.Pickup.Address,
			City:         order.Pickup.City,
			ScheduledAt:  order.Pickup.ScheduledAt.Format(time.RFC3339),
		},
		Dropoff: LocationPayload{
			CompanyName: order.Delivery.CompanyName,
			Address:     order.Delivery.Address,
			City:        order.Delivery.City,
			ScheduledAt: order.Delivery.ScheduledAt.Format(time.RFC3339),
		},
		Cargo: CargoPayload{
			PackageType:   string(order.Cargo.PackageType),
			Quantity:      order.Cargo.Quantity,
			GrossWeightKg: order.Cargo.GrossWeightKg,
			Notes:         order.Cargo.Notes,
		},
	}
}

// CreateShipment dispatches a canonical transport order to the carrier's REST API.
func (c *CarrierClient) CreateShipment(ctx context.Context, order *canonical.TransportOrder) (*ShipmentCreateResponse, error) {
	payload := FromCanonical(order)
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal carrier request: %w", err)
	}

	endpoint := fmt.Sprintf("%s/api/v1/shipments", c.baseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to build http request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if c.apiToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiToken)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("network error calling carrier api: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed reading carrier response body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("carrier api returned error HTTP %d: %s", resp.StatusCode, string(respBytes))
	}

	var result ShipmentCreateResponse
	if err := json.Unmarshal(respBytes, &result); err != nil {
		return nil, fmt.Errorf("failed to decode carrier json response: %w", err)
	}

	return &result, nil
}

// DispatchGeneric posts any JSON-serializable payload to an arbitrary endpoint with optional bearer auth.
func (c *CarrierClient) DispatchGeneric(ctx context.Context, endpointURL, token string, payload any) (map[string]any, error) {
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed marshaling payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpointURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed building http request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	} else if c.apiToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiToken)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("network error during dispatch: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed reading response body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("endpoint returned HTTP %d: %s", resp.StatusCode, string(respBytes))
	}

	if len(bytes.TrimSpace(respBytes)) == 0 {
		return map[string]any{"status": "ok"}, nil
	}

	var res map[string]any
	if err := json.Unmarshal(respBytes, &res); err != nil {
		return map[string]any{"status": "ok", "raw_response": string(respBytes)}, nil
	}
	return res, nil
}
