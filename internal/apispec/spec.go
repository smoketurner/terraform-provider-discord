// Package apispec measures the provider's Discord API coverage against
// Discord's OpenAPI spec (github.com/discord/discord-api-spec). The spec is a
// public preview, so it is used to detect new operations and fields, not to
// generate code.
package apispec

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"os"
	"slices"
	"strings"
)

// Spec is the subset of an OpenAPI 3.1 document the coverage checks read.
type Spec struct {
	Operations map[string]*Operation
	Schemas    map[string]*Schema
}

// Operation is one method on one path.
type Operation struct {
	ID          string                `json:"operationId"`
	Summary     string                `json:"summary"`
	Description string                `json:"description"`
	Parameters  []Parameter           `json:"parameters"`
	RequestBody *RequestBody          `json:"requestBody"`
	Security    []map[string][]string `json:"security"`
	Method      string                `json:"-"`
	Path        string                `json:"-"`
}

// Parameter is a path or query parameter.
type Parameter struct {
	Name string `json:"name"`
	In   string `json:"in"`
}

// RequestBody is an operation's request body by content type.
type RequestBody struct {
	Content map[string]struct {
		Schema *Schema `json:"schema"`
	} `json:"content"`
}

// Schema is the subset of a JSON Schema the checks need.
type Schema struct {
	Ref        string             `json:"$ref"`
	Type       typeList           `json:"type"`
	Const      any                `json:"const"`
	Properties map[string]*Schema `json:"properties"`
	Items      *Schema            `json:"items"`
	OneOf      []*Schema          `json:"oneOf"`
	AnyOf      []*Schema          `json:"anyOf"`
	AllOf      []*Schema          `json:"allOf"`
}

// typeList is a JSON Schema "type", which is a string or a list of strings.
type typeList []string

func (t *typeList) UnmarshalJSON(b []byte) error {
	var one string
	if err := json.Unmarshal(b, &one); err == nil {
		*t = typeList{one}
		return nil
	}
	var many []string
	if err := json.Unmarshal(b, &many); err != nil {
		return fmt.Errorf("schema type must be a string or a list of strings: %w", err)
	}
	*t = many
	return nil
}

var httpMethods = map[string]string{
	"get":    http.MethodGet,
	"put":    http.MethodPut,
	"post":   http.MethodPost,
	"patch":  http.MethodPatch,
	"delete": http.MethodDelete,
}

// LoadSpec reads an OpenAPI document from a file.
func LoadSpec(path string) (*Spec, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading spec: %w", err)
	}
	return ParseSpec(b)
}

// ParseSpec parses an OpenAPI document.
func ParseSpec(b []byte) (*Spec, error) {
	var doc struct {
		Paths      map[string]map[string]json.RawMessage `json:"paths"`
		Components struct {
			Schemas map[string]*Schema `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("parsing spec: %w", err)
	}
	if len(doc.Paths) == 0 {
		return nil, errors.New("parsing spec: no paths")
	}
	s := &Spec{Operations: map[string]*Operation{}, Schemas: doc.Components.Schemas}
	for path, item := range doc.Paths {
		for key, raw := range item {
			method, ok := httpMethods[key]
			if !ok {
				continue
			}
			var op Operation
			if err := json.Unmarshal(raw, &op); err != nil {
				return nil, fmt.Errorf("parsing %s %s: %w", method, path, err)
			}
			if op.ID == "" {
				return nil, fmt.Errorf("%s %s has no operationId", method, path)
			}
			if prev, dup := s.Operations[op.ID]; dup {
				return nil, fmt.Errorf("operationId %s is used by %s %s and %s %s", op.ID, prev.Method, prev.Path, method, path)
			}
			op.Method, op.Path = method, path
			s.Operations[op.ID] = &op
		}
	}
	return s, nil
}

// Match returns the operation for a request. Path parameters in the request
// path are written as "{}", and "{}" matches only a parameter in the spec, so
// a literal segment such as "search" or "@me" never matches a parameter.
func (s *Spec) Match(method, path string) (*Operation, bool) {
	want := templateSegments(path)
	for _, op := range s.Operations {
		if op.Method == method && slices.Equal(templateSegments(op.Path), want) {
			return op, true
		}
	}
	return nil, false
}

func templateSegments(path string) []string {
	segs := strings.Split(strings.Trim(path, "/"), "/")
	for i, seg := range segs {
		if strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}") {
			segs[i] = "{}"
		}
	}
	return segs
}

// Schema returns a named component schema.
func (s *Spec) Schema(name string) (*Schema, bool) {
	sc, ok := s.Schemas[name]
	return sc, ok
}

// resolve follows $ref chains to a component schema. Unknown references
// resolve to nil.
func (s *Spec) resolve(sc *Schema) *Schema {
	for seen := 0; sc != nil && sc.Ref != ""; seen++ {
		if seen > 32 {
			return nil
		}
		sc = s.Schemas[strings.TrimPrefix(sc.Ref, "#/components/schemas/")]
	}
	return sc
}

// Types returns the JSON types a schema accepts, looking through references
// and unions. Enums written as a list of consts take the type of their values.
func (s *Spec) Types(sc *Schema) []string {
	set := map[string]bool{}
	s.collectTypes(sc, set, 0)
	return slices.Sorted(maps.Keys(set))
}

func (s *Spec) collectTypes(sc *Schema, set map[string]bool, depth int) {
	sc = s.resolve(sc)
	if sc == nil || depth > 32 {
		return
	}
	for _, t := range sc.Type {
		set[t] = true
	}
	switch sc.Const.(type) {
	case string:
		set["string"] = true
	case float64:
		set["integer"] = true
	case bool:
		set["boolean"] = true
	}
	for _, group := range [][]*Schema{sc.OneOf, sc.AnyOf, sc.AllOf} {
		for _, sub := range group {
			s.collectTypes(sub, set, depth+1)
		}
	}
}

// Properties returns the object properties a schema allows, merged across
// references and unions. For an array it returns the properties of its items.
func (s *Spec) Properties(sc *Schema) map[string]*Schema {
	props := map[string]*Schema{}
	s.collectProperties(sc, props, 0)
	return props
}

func (s *Spec) collectProperties(sc *Schema, props map[string]*Schema, depth int) {
	sc = s.resolve(sc)
	if sc == nil || depth > 32 {
		return
	}
	for name, p := range sc.Properties {
		if _, ok := props[name]; !ok {
			props[name] = p
		}
	}
	if sc.Items != nil {
		s.collectProperties(sc.Items, props, depth+1)
	}
	for _, group := range [][]*Schema{sc.OneOf, sc.AnyOf, sc.AllOf} {
		for _, sub := range group {
			s.collectProperties(sub, props, depth+1)
		}
	}
}

// Items returns the item schema of an array schema, looking through
// references and unions.
func (s *Spec) Items(sc *Schema) *Schema {
	sc = s.resolve(sc)
	if sc == nil {
		return nil
	}
	if sc.Items != nil {
		return sc.Items
	}
	for _, group := range [][]*Schema{sc.OneOf, sc.AnyOf, sc.AllOf} {
		for _, sub := range group {
			if items := s.Items(sub); items != nil {
				return items
			}
		}
	}
	return nil
}

// bodyContentTypes are the request content types read for body fields, in
// order of preference. Some uploads accept only multipart bodies.
var bodyContentTypes = []string{"application/json", "multipart/form-data"}

// RequestFields lists an operation's query parameters (prefixed with "?")
// and the top-level fields of its request body, sorted.
func (s *Spec) RequestFields(op *Operation) []string {
	set := map[string]bool{}
	for _, p := range op.Parameters {
		if p.In == "query" {
			set["?"+p.Name] = true
		}
	}
	if op.RequestBody != nil {
		for _, ct := range bodyContentTypes {
			if c, ok := op.RequestBody.Content[ct]; ok {
				for name := range s.Properties(c.Schema) {
					set[name] = true
				}
				break
			}
		}
	}
	return slices.Sorted(maps.Keys(set))
}
