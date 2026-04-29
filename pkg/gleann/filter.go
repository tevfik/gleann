// Package gleann provides metadata filtering for search results.
package gleann

import (
	"fmt"
	"strings"
)

// FilterOperator defines a metadata filter comparison operator.
type FilterOperator string

const (
	OpEqual        FilterOperator = "eq"
	OpNotEqual     FilterOperator = "ne"
	OpGreaterThan  FilterOperator = "gt"
	OpGreaterEqual FilterOperator = "gte"
	OpLessThan     FilterOperator = "lt"
	OpLessEqual    FilterOperator = "lte"
	OpIn           FilterOperator = "in"
	OpNotIn        FilterOperator = "nin"
	OpContains     FilterOperator = "contains"
	OpStartsWith   FilterOperator = "startswith"
	OpEndsWith     FilterOperator = "endswith"
	OpExists       FilterOperator = "exists"
)

// MetadataFilter represents a single filter condition.
type MetadataFilter struct {
	Field    string         `json:"field"`
	Operator FilterOperator `json:"operator"`
	Value    any            `json:"value"`
}

// MetadataFilterEngine evaluates filters against metadata.
type MetadataFilterEngine struct {
	Filters []MetadataFilter
	Logic   string // "and" or "or" (default: "and")
}

// NewMetadataFilterEngine creates a new filter engine.
func NewMetadataFilterEngine(filters []MetadataFilter) *MetadataFilterEngine {
	return &MetadataFilterEngine{
		Filters: filters,
		Logic:   "and",
	}
}

// Match evaluates all filters against the given metadata.
func (e *MetadataFilterEngine) Match(metadata map[string]any) bool {
	if len(e.Filters) == 0 {
		return true
	}

	for _, f := range e.Filters {
		result := evaluateFilter(f, metadata)
		if e.Logic == "or" {
			if result {
				return true
			}
		} else {
			// "and" logic
			if !result {
				return false
			}
		}
	}

	return e.Logic != "or"
}

// FilterResults filters search results based on metadata filters.
func (e *MetadataFilterEngine) FilterResults(results []SearchResult) []SearchResult {
	if len(e.Filters) == 0 {
		return results
	}
	var filtered []SearchResult
	for _, r := range results {
		if e.Match(r.Metadata) {
			filtered = append(filtered, r)
		}
	}
	return filtered
}

// evaluateFilter evaluates a single filter condition.
func evaluateFilter(f MetadataFilter, metadata map[string]any) bool {
	value, exists := metadata[f.Field]

	// Handle exists operator.
	if f.Operator == OpExists {
		want, _ := f.Value.(bool)
		return exists == want
	}

	if !exists {
		return false
	}

	switch f.Operator {
	case OpEqual:
		return compareEqual(value, f.Value)
	case OpNotEqual:
		return !compareEqual(value, f.Value)
	case OpGreaterThan:
		return compareNumeric(value, f.Value) > 0
	case OpGreaterEqual:
		return compareNumeric(value, f.Value) >= 0
	case OpLessThan:
		return compareNumeric(value, f.Value) < 0
	case OpLessEqual:
		return compareNumeric(value, f.Value) <= 0
	case OpIn:
		return compareIn(value, f.Value)
	case OpNotIn:
		return !compareIn(value, f.Value)
	case OpContains:
		return strings.Contains(fmt.Sprint(value), fmt.Sprint(f.Value))
	case OpStartsWith:
		return strings.HasPrefix(fmt.Sprint(value), fmt.Sprint(f.Value))
	case OpEndsWith:
		return strings.HasSuffix(fmt.Sprint(value), fmt.Sprint(f.Value))
	default:
		return false
	}
}

// compareEqual compares two values for equality.
func compareEqual(a, b any) bool {
	return fmt.Sprint(a) == fmt.Sprint(b)
}

// compareNumeric compares two values numerically.
// Returns -1, 0, or 1 (like strcmp).
func compareNumeric(a, b any) int {
	fa := toFloat64(a)
	fb := toFloat64(b)
	if fa < fb {
		return -1
	}
	if fa > fb {
		return 1
	}
	return 0
}

// compareIn checks if value is in the list.
func compareIn(value, list any) bool {
	target := fmt.Sprint(value)
	switch v := list.(type) {
	case []any:
		for _, item := range v {
			if fmt.Sprint(item) == target {
				return true
			}
		}
	case []string:
		for _, item := range v {
			if item == target {
				return true
			}
		}
	}
	return false
}

// toFloat64 converts a value to float64 for numeric comparison.
func toFloat64(v any) float64 {
	switch n := v.(type) {
	case int:
		return float64(n)
	case int64:
		return float64(n)
	case float64:
		return n
	case float32:
		return float64(n)
	case string:
		var f float64
		fmt.Sscanf(n, "%f", &f)
		return f
	default:
		return 0
	}
}

// WithMetadataFilter adds metadata filtering to search.
func WithMetadataFilter(filters ...MetadataFilter) SearchOption {
	return func(c *SearchConfig) {
		c.MetadataFilters = filters
	}
}

// WithFilterLogic sets the filter logic ("and" or "or").
func WithFilterLogic(logic string) SearchOption {
	return func(c *SearchConfig) {
		c.FilterLogic = logic
	}
}
