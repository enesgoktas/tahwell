package mapping

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"
)

// ErrUnsupportedFormat is returned when a payload format cannot be determined.
var ErrUnsupportedFormat = errors.New("unsupported or unrecognized payload format")

// ParseJSON parses raw JSON bytes into a generic nested map.
func ParseJSON(data []byte) (map[string]any, error) {
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("failed parsing JSON payload: %w", err)
	}
	return result, nil
}

type xmlElement struct {
	name     string
	text     strings.Builder
	children []*xmlElement
}

// ParseXML parses arbitrary XML (including SOAP envelopes) into a generic nested map.
// Local element names are used (namespace prefixes are stripped).
func ParseXML(data []byte) (map[string]any, error) {
	decoder := xml.NewDecoder(bytes.NewReader(data))

	var stack []*xmlElement
	var root *xmlElement

	for {
		token, err := decoder.Token()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("XML syntax error: %w", err)
		}

		switch tok := token.(type) {
		case xml.StartElement:
			name := tok.Name.Local
			elem := &xmlElement{name: name}
			if len(stack) > 0 {
				parent := stack[len(stack)-1]
				parent.children = append(parent.children, elem)
			} else if root == nil {
				root = elem
			}
			stack = append(stack, elem)

		case xml.CharData:
			if len(stack) > 0 {
				stack[len(stack)-1].text.Write(tok)
			}

		case xml.EndElement:
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		}
	}

	if root == nil {
		return nil, errors.New("empty XML document")
	}

	nodeMap := xmlNodeToMap(root)
	result, ok := nodeMap[root.name].(map[string]any)
	if !ok {
		result = nodeMap
	}

	// Convenience for SOAP envelopes: if Envelope has Body, expose inner fields at root as well
	elevateSOAPBody(result)

	return result, nil
}

func xmlNodeToMap(elem *xmlElement) map[string]any {
	result := make(map[string]any)

	if len(elem.children) == 0 {
		text := strings.TrimSpace(elem.text.String())
		result[elem.name] = text
		return result
	}

	childMap := make(map[string]any)
	for _, child := range elem.children {
		converted := xmlNodeToMap(child)
		childVal := converted[child.name]

		if existing, found := childMap[child.name]; found {
			switch arr := existing.(type) {
			case []any:
				childMap[child.name] = append(arr, childVal)
			default:
				childMap[child.name] = []any{arr, childVal}
			}
		} else {
			childMap[child.name] = childVal
		}
	}

	result[elem.name] = childMap
	return result
}

// elevateSOAPBody checks if this map contains Envelope.Body and lifts child request fields to the top level.
func elevateSOAPBody(m map[string]any) {
	bodyVal, hasBody := m["Body"]
	if !hasBody {
		return
	}
	bodyMap, ok := bodyVal.(map[string]any)
	if !ok {
		return
	}

	// Copy all fields inside Body and its first-level child requests to top level
	for k, v := range bodyMap {
		if _, exists := m[k]; !exists {
			m[k] = v
		}
		if childReqMap, isMap := v.(map[string]any); isMap {
			for subK, subV := range childReqMap {
				if _, exists := m[subK]; !exists {
					m[subK] = subV
				}
			}
		}
	}
}

// ParseAuto sniffs the content-type or first non-whitespace byte to parse JSON or XML.
func ParseAuto(contentType string, data []byte) (map[string]any, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return nil, errors.New("empty payload")
	}

	lowerCT := strings.ToLower(contentType)
	if strings.Contains(lowerCT, "xml") {
		return ParseXML(trimmed)
	}
	if strings.Contains(lowerCT, "json") {
		return ParseJSON(trimmed)
	}

	// Sniff first character
	switch trimmed[0] {
	case '<':
		return ParseXML(trimmed)
	case '{', '[':
		return ParseJSON(trimmed)
	default:
		return nil, fmt.Errorf("%w: cannot infer from content-type %q or payload prefix", ErrUnsupportedFormat, contentType)
	}
}
