package profile

import (
	"fmt"
	"os"
	"sync"

	"github.com/goktasenes/tahwell/pkg/mapping"
	"gopkg.in/yaml.v3"
)

// Profile configures an end-to-end integration mapping between a shipper ERP and carrier.
type Profile struct {
	ID                 string               `yaml:"id" json:"id"`
	Name               string               `yaml:"name" json:"name"`
	Shipper            string               `yaml:"shipper" json:"shipper"`
	Carrier            string               `yaml:"carrier" json:"carrier"`
	CarrierEndpointURL string               `yaml:"carrier_endpoint_url" json:"carrier_endpoint_url"`
	CarrierAuthToken   string               `yaml:"carrier_auth_token" json:"carrier_auth_token"`
	ERPCallbackURL     string               `yaml:"erp_callback_url,omitempty" json:"erp_callback_url,omitempty"`
	InboundOrderSpec   *mapping.MappingSpec `yaml:"inbound_order_spec,omitempty" json:"inbound_order_spec,omitempty"`
	OutboundOrderSpec  *mapping.MappingSpec `yaml:"outbound_order_spec,omitempty" json:"outbound_order_spec,omitempty"`
	InboundStatusSpec  *mapping.MappingSpec `yaml:"inbound_status_spec,omitempty" json:"inbound_status_spec,omitempty"`
	OutboundStatusSpec *mapping.MappingSpec `yaml:"outbound_status_spec,omitempty" json:"outbound_status_spec,omitempty"`
}

// Registry manages in-memory loaded integration profiles.
type Registry struct {
	mu       sync.RWMutex
	profiles map[string]*Profile
}

// NewRegistry initializes an empty Profile Registry.
func NewRegistry() *Registry {
	return &Registry{
		profiles: make(map[string]*Profile),
	}
}

// Register adds or updates a profile in the registry.
func (r *Registry) Register(p *Profile) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.profiles[p.ID] = p
}

// Get retrieves a profile by ID.
func (r *Registry) Get(id string) (*Profile, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.profiles[id]
	return p, ok
}

// List returns a slice of all registered profiles.
func (r *Registry) List() []*Profile {
	r.mu.RLock()
	defer r.mu.RUnlock()
	list := make([]*Profile, 0, len(r.profiles))
	for _, p := range r.profiles {
		list = append(list, p)
	}
	return list
}

// LoadProfileFromYAML reads a profile from raw YAML bytes.
func LoadProfileFromYAML(data []byte) (*Profile, error) {
	var p Profile
	if err := yaml.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("failed parsing profile YAML: %w", err)
	}
	if p.ID == "" {
		return nil, fmt.Errorf("profile id is required")
	}
	return &p, nil
}

// LoadProfileFromFile reads a profile from a YAML file on disk.
func LoadProfileFromFile(path string) (*Profile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed reading profile file: %w", err)
	}
	return LoadProfileFromYAML(data)
}
