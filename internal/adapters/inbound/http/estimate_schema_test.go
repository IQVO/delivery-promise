package http

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// openAPISchemas loads components.schemas of apis/openapi.yaml.
func openAPISchemas(t *testing.T) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "apis", "openapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	return doc["components"].(map[string]any)["schemas"].(map[string]any)
}

// schemaCheck validates a decoded JSON value against the subset of OpenAPI
// 3.0 the estimate schemas use ($ref, type, required, properties, items,
// enum, minimum, maximum, minItems, maxItems, format date-time), and refuses
// any field a schema does not declare: the response may not carry an
// undocumented property.
type schemaCheck struct {
	schemas map[string]any
	errs    []string
}

func (c *schemaCheck) fail(at, format string, args ...any) {
	c.errs = append(c.errs, at+": "+fmt.Sprintf(format, args...))
}

func (c *schemaCheck) resolve(s map[string]any) map[string]any {
	if ref, ok := s["$ref"].(string); ok {
		return c.schemas[strings.TrimPrefix(ref, "#/components/schemas/")].(map[string]any)
	}
	return s
}

func (c *schemaCheck) value(at string, s map[string]any, v any) {
	s = c.resolve(s)
	if enum, ok := s["enum"].([]any); ok && !slices.Contains(enum, v) {
		c.fail(at, "%v is not one of %v", v, enum)
	}
	switch s["type"] {
	case "object":
		c.object(at, s, v)
	case "array":
		c.array(at, s, v)
	case "string":
		c.str(at, s, v)
	case "integer":
		c.integer(at, s, v)
	}
}

func (c *schemaCheck) object(at string, s map[string]any, v any) {
	obj, ok := v.(map[string]any)
	if !ok {
		c.fail(at, "want an object, got %T", v)
		return
	}
	props, _ := s["properties"].(map[string]any)
	for _, r := range s["required"].([]any) {
		if _, has := obj[r.(string)]; !has {
			c.fail(at, "required %q is missing", r)
		}
	}
	for k, fv := range obj {
		ps, declared := props[k].(map[string]any)
		if !declared {
			c.fail(at, "%q is not declared by the schema", k)
			continue
		}
		c.value(at+"."+k, ps, fv)
	}
}

func (c *schemaCheck) array(at string, s map[string]any, v any) {
	arr, ok := v.([]any)
	if !ok {
		c.fail(at, "want an array, got %T", v)
		return
	}
	if lo, ok := s["minItems"].(int); ok && len(arr) < lo {
		c.fail(at, "%d items, minItems %d", len(arr), lo)
	}
	if hi, ok := s["maxItems"].(int); ok && len(arr) > hi {
		c.fail(at, "%d items, maxItems %d", len(arr), hi)
	}
	for i, e := range arr {
		c.value(fmt.Sprintf("%s[%d]", at, i), s["items"].(map[string]any), e)
	}
}

func (c *schemaCheck) str(at string, s map[string]any, v any) {
	str, ok := v.(string)
	if !ok {
		c.fail(at, "want a string, got %T", v)
		return
	}
	if s["format"] == "date-time" {
		if _, err := time.Parse(time.RFC3339, str); err != nil {
			c.fail(at, "%q is not RFC 3339", str)
		}
	}
}

func (c *schemaCheck) integer(at string, s map[string]any, v any) {
	n, ok := v.(float64)
	if !ok || n != float64(int(n)) {
		c.fail(at, "want an integer, got %v", v)
		return
	}
	if lo, ok := s["minimum"].(int); ok && int(n) < lo {
		c.fail(at, "%v below minimum %d", n, lo)
	}
	if hi, ok := s["maximum"].(int); ok && int(n) > hi {
		c.fail(at, "%v above maximum %d", n, hi)
	}
}

func validateAgainst(t *testing.T, schema, body string) []string {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		t.Fatal(err)
	}
	c := &schemaCheck{schemas: openAPISchemas(t)}
	c.value(schema, map[string]any{"$ref": "#/components/schemas/" + schema}, v)
	return c.errs
}

// The pinned response conforms to EstimateResponse in apis/openapi.yaml and
// the example request to EstimateRequest: the spec and the adapter agree.
func TestTheEstimateGoldenConformsToTheOpenAPISchema(t *testing.T) {
	golden, err := os.ReadFile(filepath.Join("testdata", "estimate_response.golden.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range validateAgainst(t, "EstimateResponse", string(golden)) {
		t.Error(e)
	}
	for _, e := range validateAgainst(t, "EstimateRequest", estimateBody) {
		t.Error(e)
	}
}

// The checker itself can fail: an undeclared field, a missing required one
// and an out-of-vocabulary constraint code are all reported.
func TestTheSchemaCheckFailsOnDrift(t *testing.T) {
	bad := `{"computedAt":"2026-10-22T19:05:00Z","estimates":[{"catalogItemId":"c","quantity":1000,"options":[],` +
		`"constraints":[{"code":"SOLD_OUT"}],"offerId":"off-1"}]}`
	got := strings.Join(validateAgainst(t, "EstimateResponse", bad), "\n")
	for _, want := range []string{`"destinationPostalPrefix" is missing`, `"offerId" is not declared`, "SOLD_OUT is not one of", "above maximum 999"} {
		if !strings.Contains(got, want) {
			t.Errorf("drift %q not reported in:\n%s", want, got)
		}
	}
	if errs := validateAgainst(t, "EstimateRequest", `{"destinationPostalPrefix":"787","items":[]}`); len(errs) != 1 {
		t.Errorf("an empty items list: %v", errs)
	}
}
