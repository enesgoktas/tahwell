package mapping

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Engine executes declarative mapping specifications.
type Engine struct{}

// NewEngine returns a new Mapping Engine instance.
func NewEngine() *Engine {
	return &Engine{}
}

// LoadSpecFromYAML parses a mapping spec from raw YAML bytes.
func LoadSpecFromYAML(data []byte) (*MappingSpec, error) {
	var spec MappingSpec
	if err := yaml.Unmarshal(data, &spec); err != nil {
		return nil, fmt.Errorf("failed to parse mapping yaml: %w", err)
	}
	return &spec, nil
}

// LoadSpecFromFile reads and parses a YAML mapping spec from disk.
func LoadSpecFromFile(path string) (*MappingSpec, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read mapping file: %w", err)
	}
	return LoadSpecFromYAML(content)
}

// Transform executes the spec rules on input map, producing the target map.
func (e *Engine) Transform(spec *MappingSpec, input map[string]any) (map[string]any, error) {
	if spec == nil {
		return nil, fmt.Errorf("mapping spec is nil")
	}
	output := make(map[string]any)

	for _, rule := range spec.Rules {
		val, found := getPath(input, rule.SourcePath)
		if !found || val == nil || val == "" {
			if rule.Default != nil {
				val = rule.Default
			} else if rule.Required {
				return nil, fmt.Errorf("required field %q is missing in source", rule.SourcePath)
			} else {
				continue
			}
		}

		// Apply Lookup if specified
		if rule.Lookup != "" && spec.Lookups != nil {
			if table, ok := spec.Lookups[rule.Lookup]; ok {
				keyStr := fmt.Sprintf("%v", val)
				if lookupVal, matched := table[keyStr]; matched {
					val = lookupVal
				}
			}
		}

		// Apply Transform if specified
		if rule.Transform != "" {
			transformed, err := applyTransform(val, rule.Transform)
			if err != nil {
				return nil, fmt.Errorf("transform error on field %q: %w", rule.SourcePath, err)
			}
			val = transformed
		}

		setPath(output, rule.TargetPath, val)
	}

	return output, nil
}

// getPath navigates a dot-separated path in nested maps.
func getPath(data map[string]any, path string) (any, bool) {
	if path == "" {
		return nil, false
	}
	parts := strings.Split(path, ".")
	var current any = data

	for _, part := range parts {
		m, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		val, exists := m[part]
		if !exists {
			return nil, false
		}
		current = val
	}
	return current, true
}

// setPath sets a value at a dot-separated path, creating parent maps as needed.
func setPath(data map[string]any, path string, value any) {
	parts := strings.Split(path, ".")
	current := data

	for i := 0; i < len(parts)-1; i++ {
		part := parts[i]
		sub, ok := current[part].(map[string]any)
		if !ok {
			sub = make(map[string]any)
			current[part] = sub
		}
		current = sub
	}
	current[parts[len(parts)-1]] = value
}

// applyTransform handles string, numeric, and date transforms.
func applyTransform(val any, transform string) (any, error) {
	parts := strings.SplitN(transform, ":", 2)
	op := parts[0]
	arg := ""
	if len(parts) > 1 {
		arg = parts[1]
	}

	switch op {
	case "trim":
		return strings.TrimSpace(fmt.Sprintf("%v", val)), nil

	case "uppercase":
		return strings.ToUpper(fmt.Sprintf("%v", val)), nil

	case "lowercase":
		return strings.ToLower(fmt.Sprintf("%v", val)), nil

	case "to_string":
		return fmt.Sprintf("%v", val), nil

	case "to_int":
		return toInt(val)

	case "to_float":
		return toFloat(val)

	case "multiply":
		factor, err := strconv.ParseFloat(arg, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid multiplier factor %q: %w", arg, err)
		}
		num, err := toFloat(val)
		if err != nil {
			return nil, err
		}
		return num * factor, nil

	case "divide":
		divisor, err := strconv.ParseFloat(arg, 64)
		if err != nil || divisor == 0 {
			return nil, fmt.Errorf("invalid divisor %q", arg)
		}
		num, err := toFloat(val)
		if err != nil {
			return nil, err
		}
		return num / divisor, nil

	case "date_iso8601":
		return parseDateToISO8601(val, arg)

	default:
		return val, nil
	}
}

func toFloat(val any) (float64, error) {
	switch v := val.(type) {
	case float64:
		return v, nil
	case float32:
		return float64(v), nil
	case int:
		return float64(v), nil
	case int64:
		return float64(v), nil
	case string:
		clean := strings.TrimSpace(v)
		clean = strings.ReplaceAll(clean, ",", ".")
		return strconv.ParseFloat(clean, 64)
	default:
		return 0, fmt.Errorf("cannot convert %T to float", val)
	}
}

func toInt(val any) (int, error) {
	f, err := toFloat(val)
	if err != nil {
		return 0, err
	}
	return int(f), nil
}

// parseDateToISO8601 parses various common logistics date representations to RFC3339.
func parseDateToISO8601(val any, layout string) (string, error) {
	str := strings.TrimSpace(fmt.Sprintf("%v", val))
	if str == "" {
		return "", nil
	}

	// Try custom layout if provided
	if layout != "" {
		t, err := time.Parse(layout, str)
		if err == nil {
			return t.UTC().Format(time.RFC3339), nil
		}
	}

	commonLayouts := []string{
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
		"2006-01-02 15:04",
		"02.01.2006 15:04:05",
		"02.01.2006 15:04",
		"02.01.2006",
		"2006-01-02",
	}

	for _, l := range commonLayouts {
		if t, err := time.Parse(l, str); err == nil {
			return t.UTC().Format(time.RFC3339), nil
		}
	}

	return "", fmt.Errorf("unable to parse date string %q", str)
}
