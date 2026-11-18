package mapping

// TransformType represents the transformation applied to a value.
type TransformType string

const (
	TransformNone       TransformType = "none"
	TransformMultiply   TransformType = "multiply"
	TransformDivide     TransformType = "divide"
	TransformToInt      TransformType = "to_int"
	TransformToFloat    TransformType = "to_float"
	TransformToString   TransformType = "to_string"
	TransformTrim       TransformType = "trim"
	TransformUpper      TransformType = "uppercase"
	TransformLower      TransformType = "lowercase"
	TransformDateISO    TransformType = "date_iso8601"
)

// FieldRule describes how a single source field maps to a target field.
type FieldRule struct {
	// SourcePath is the dot-separated path in the input data (e.g. "Order.Details.Weight").
	SourcePath string `yaml:"source" json:"source"`

	// TargetPath is the dot-separated path in the output data (e.g. "cargo.gross_weight_kg").
	TargetPath string `yaml:"target" json:"target"`

	// Transform specifies an optional operation, e.g. "multiply:1000", "date_iso8601", "trim".
	Transform string `yaml:"transform,omitempty" json:"transform,omitempty"`

	// Lookup references a named lookup table defined in the spec.
	Lookup string `yaml:"lookup,omitempty" json:"lookup,omitempty"`

	// Default is the fallback value if the source path resolves to nil or empty string.
	Default any `yaml:"default,omitempty" json:"default,omitempty"`

	// Required indicates if transformation should fail when value is missing and no default.
	Required bool `yaml:"required,omitempty" json:"required,omitempty"`
}

// MappingSpec defines an end-to-end declarative mapping configuration.
type MappingSpec struct {
	Name         string                    `yaml:"name" json:"name"`
	Description  string                    `yaml:"description,omitempty" json:"description,omitempty"`
	Version      string                    `yaml:"version" json:"version"`
	Lookups      map[string]map[string]any `yaml:"lookups,omitempty" json:"lookups,omitempty"`
	Rules        []FieldRule               `yaml:"rules" json:"rules"`
}
