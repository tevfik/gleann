package gleann

import (
	"encoding/json"
	"math"
	"testing"
)

// The shared validator (REST, MCP and the CLI) rejects a null or non-array
// in/nin operand and a NaN/Inf numeric operand.
func TestMetadataFilterValidateOperands(t *testing.T) {
	bad := []MetadataFilter{
		{Field: "t", Operator: OpIn, Value: nil},
		{Field: "t", Operator: OpNotIn, Value: "go"},
		{Field: "n", Operator: OpLessEqual, Value: math.NaN()},
		{Field: "n", Operator: OpGreaterThan, Value: math.Inf(1)},
	}
	for _, f := range bad {
		if err := f.Validate(); err == nil {
			t.Errorf("Validate accepted %+v", f)
		}
	}
	good := []MetadataFilter{
		{Field: "t", Operator: OpIn, Value: []any{"a"}},
		{Field: "t", Operator: OpIn, Value: []string{"a"}},
		{Field: "n", Operator: OpGreaterThan, Value: float64(3)},
		{Field: "n", Operator: OpGreaterThan, Value: "3"},
		{Field: "e", Operator: OpExists, Value: true},
	}
	for _, f := range good {
		if err := f.Validate(); err != nil {
			t.Errorf("Validate rejected %+v: %v", f, err)
		}
	}
}

// The REST/MCP wire path: a JSON filter with a null "in" value is refused.
func TestMetadataFilterJSONNullInRejected(t *testing.T) {
	var fs []MetadataFilter
	if err := json.Unmarshal([]byte(`[{"field":"tags","operator":"in","value":null}]`), &fs); err != nil {
		t.Fatal(err)
	}
	if err := ValidateMetadataFilters(fs); err == nil {
		t.Fatal("null in value accepted")
	}
}
