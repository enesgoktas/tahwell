package soap

import (
	"encoding/xml"
	"fmt"
	"strings"
	"time"

	"github.com/goktasenes/tahwell/pkg/canonical"
)

// SOAP Envelope types for Generic Enterprise ERP Web Service.
type SOAPEnvelopeRequest struct {
	XMLName xml.Name `xml:"Envelope"`
	Body    SOAPBodyRequest `xml:"Body"`
}

type SOAPBodyRequest struct {
	XMLName xml.Name `xml:"Body"`
	Request CreateTransportRequest `xml:"CreateTransportRequest"`
}

type CreateTransportRequest struct {
	XMLName         xml.Name `xml:"CreateTransportRequest"`
	OrderNumber     string   `xml:"OrderNumber"`
	DepartureDate   string   `xml:"DepartureDate"`
	PlantCode       string   `xml:"PlantCode"`
	CustomerTitle   string   `xml:"CustomerTitle"`
	DeliveryAddress string   `xml:"DeliveryAddress"`
	DeliveryCity    string   `xml:"DeliveryCity"`
	PalletCount     int      `xml:"PalletCount"`
	TotalKg         float64  `xml:"TotalKg"`
	VehicleType     string   `xml:"VehicleType"`
	SpecialNotes    string   `xml:"SpecialNotes"`
}

// SOAP Envelope response types.
type SOAPEnvelopeResponse struct {
	XMLName xml.Name         `xml:"soap:Envelope"`
	Soap    string           `xml:"xmlns:soap,attr"`
	Thw     string           `xml:"xmlns:thw,attr"`
	Body    SOAPBodyResponse `xml:"soap:Body"`
}

type SOAPBodyResponse struct {
	Response CreateTransportResponse `xml:"thw:CreateTransportResponse"`
}

type CreateTransportResponse struct {
	Success         bool   `xml:"thw:Success"`
	OrderNumber     string `xml:"thw:OrderNumber"`
	CarrierTripCode string `xml:"thw:CarrierTripCode"`
	Message         string `xml:"thw:Message"`
}

// SOAP Fault structure.
type SOAPEnvelopeFault struct {
	XMLName xml.Name      `xml:"soap:Envelope"`
	Soap    string        `xml:"xmlns:soap,attr"`
	Body    SOAPBodyFault `xml:"soap:Body"`
}

type SOAPBodyFault struct {
	Fault SOAPFault `xml:"soap:Fault"`
}

type SOAPFault struct {
	FaultCode   string `xml:"faultcode"`
	FaultString string `xml:"faultstring"`
}

// ParseSOAPRequest unmarshals raw XML into a generic CreateTransportRequest.
func ParseSOAPRequest(xmlData []byte) (*CreateTransportRequest, error) {
	var env SOAPEnvelopeRequest
	if err := xml.Unmarshal(xmlData, &env); err != nil {
		return nil, fmt.Errorf("failed to parse SOAP request: %w", err)
	}
	req := env.Body.Request
	if strings.TrimSpace(req.OrderNumber) == "" {
		return nil, fmt.Errorf("invalid SOAP body: OrderNumber is empty")
	}
	return &req, nil
}

// ToCanonical converts the generic SOAP request into Tahwell's Canonical TransportOrder.
func (r *CreateTransportRequest) ToCanonical(shipperName string, plantAddressLookup map[string]string) (*canonical.TransportOrder, error) {
	if strings.TrimSpace(shipperName) == "" {
		shipperName = "Enterprise Shipper"
	}

	pickupAddr := r.PlantCode
	if plantAddressLookup != nil {
		if addr, ok := plantAddressLookup[r.PlantCode]; ok {
			pickupAddr = addr
		}
	}

	var scheduledAt time.Time
	if strings.TrimSpace(r.DepartureDate) != "" {
		parsed, err := time.Parse("2006-01-02T15:04:05", strings.TrimSpace(r.DepartureDate))
		if err != nil {
			parsed, err = time.Parse(time.RFC3339, strings.TrimSpace(r.DepartureDate))
		}
		if err == nil {
			scheduledAt = parsed.UTC()
		}
	}
	if scheduledAt.IsZero() {
		scheduledAt = time.Now().UTC().Add(24 * time.Hour)
	}

	order := &canonical.TransportOrder{
		ReferenceID: r.OrderNumber,
		ShipperName: shipperName,
		Status:      canonical.StatusDraft,
		CreatedAt:   time.Now().UTC(),
		Pickup: canonical.Location{
			FacilityCode: r.PlantCode,
			CompanyName:  shipperName + " Central Facility",
			Address:      pickupAddr,
			City:         "Origin City",
			ScheduledAt:  scheduledAt,
		},
		Delivery: canonical.Location{
			CompanyName: r.CustomerTitle,
			Address:     r.DeliveryAddress,
			City:        r.DeliveryCity,
			ScheduledAt: scheduledAt.Add(24 * time.Hour),
		},
		Cargo: canonical.CargoDetails{
			PackageType:       canonical.PackagePallet,
			Quantity:          r.PalletCount,
			GrossWeightKg:     r.TotalKg,
			TemperatureRegime: canonical.TempDry,
			Notes:             r.SpecialNotes,
		},
		Vehicle: canonical.VehicleReqs{
			VehicleType: r.VehicleType,
		},
	}

	if err := order.Validate(); err != nil {
		return nil, fmt.Errorf("canonical validation failed: %w", err)
	}

	return order, nil
}

// BuildSOAPResponse formats a successful transport creation into a SOAP response.
func BuildSOAPResponse(orderNumber, carrierTripCode, message string) ([]byte, error) {
	resp := SOAPEnvelopeResponse{
		Soap: "http://schemas.xmlsoap.org/soap/envelope/",
		Thw:  "http://tahwell.io/schema/transport/v1",
		Body: SOAPBodyResponse{
			Response: CreateTransportResponse{
				Success:         true,
				OrderNumber:     orderNumber,
				CarrierTripCode: carrierTripCode,
				Message:         message,
			},
		},
	}
	data, err := xml.MarshalIndent(resp, "", "  ")
	if err != nil {
		return nil, err
	}
	return append([]byte(xml.Header), data...), nil
}

// BuildSOAPFault formats a SOAP Fault response.
func BuildSOAPFault(faultString string) ([]byte, error) {
	fault := SOAPEnvelopeFault{
		Soap: "http://schemas.xmlsoap.org/soap/envelope/",
		Body: SOAPBodyFault{
			Fault: SOAPFault{
				FaultCode:   "soap:Client",
				FaultString: faultString,
			},
		},
	}
	data, err := xml.MarshalIndent(fault, "", "  ")
	if err != nil {
		return nil, err
	}
	return append([]byte(xml.Header), data...), nil
}
