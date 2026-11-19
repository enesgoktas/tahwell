package profile

import (
	"testing"
)

func TestProfileRegistry(t *testing.T) {
	yamlData := `
id: "sap_to_carrier_alpha"
name: "SAP to Carrier Alpha Integration"
shipper: "Enterprise Corp"
carrier: "Carrier Alpha"
carrier_endpoint_url: "https://api.carrieralpha.com/shipments"
carrier_auth_token: "secret_token_123"
erp_callback_url: "https://erp.enterprise.com/status"
inbound_order_spec:
  name: "sap_inbound"
  version: "1.0.0"
  rules:
    - source: "OrderNo"
      target: "reference_id"
`

	p, err := LoadProfileFromYAML([]byte(yamlData))
	if err != nil {
		t.Fatalf("LoadProfileFromYAML failed: %v", err)
	}

	if p.ID != "sap_to_carrier_alpha" {
		t.Errorf("expected sap_to_carrier_alpha, got %v", p.ID)
	}
	if p.CarrierEndpointURL != "https://api.carrieralpha.com/shipments" {
		t.Errorf("unexpected carrier endpoint: %v", p.CarrierEndpointURL)
	}
	if p.InboundOrderSpec == nil || len(p.InboundOrderSpec.Rules) != 1 {
		t.Errorf("inbound spec not loaded properly")
	}

	reg := NewRegistry()
	reg.Register(p)

	fetched, ok := reg.Get("sap_to_carrier_alpha")
	if !ok || fetched.Name != "SAP to Carrier Alpha Integration" {
		t.Errorf("failed fetching profile from registry")
	}

	if len(reg.List()) != 1 {
		t.Errorf("expected 1 profile, got %d", len(reg.List()))
	}
}
