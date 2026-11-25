package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/tevfik/gleann/pkg/gleann"
)

// parseCLIFilter turns a single --filter expression into a MetadataFilter.
// The grammar is "field op value"; the unary "exists" operator may omit the
// value (e.g. "lang exists"). The value is decoded as JSON when it parses
// (number, bool, array, object, quoted string) and kept as a raw string
// otherwise, so --filter 'loc gt 100' compares a number while
// --filter 'lang eq go' compares a string.
func parseCLIFilter(expr string) (gleann.MetadataFilter, error) {
	parts := strings.Fields(expr)
	if len(parts) < 2 {
		return gleann.MetadataFilter{}, fmt.Errorf(
			"--filter %q: expected 'field op value' (operators: eq, ne, gt, gte, lt, lte, in, nin, contains, startswith, endswith, exists)",
			expr,
		)
	}
	f := gleann.MetadataFilter{
		Field:    parts[0],
		Operator: gleann.FilterOperator(strings.ToLower(parts[1])),
	}
	if len(parts) > 2 {
		raw := strings.Join(parts[2:], " ")
		var decoded any
		if err := json.Unmarshal([]byte(raw), &decoded); err == nil {
			f.Value = decoded
		} else {
			f.Value = raw
		}
	}
	if err := f.Validate(); err != nil {
		return gleann.MetadataFilter{}, fmt.Errorf("--filter %q: %w", expr, err)
	}
	return f, nil
}

// parseCLIFilters parses every repeatable --filter flag in args into
// validated metadata filters. It returns nil (no filters) when none are
// given, so callers can append the option unconditionally only when the
// slice is non-empty.
func parseCLIFilters(args []string) ([]gleann.MetadataFilter, error) {
	vals := getAllFlags(args, "--filter")
	if len(vals) == 0 {
		return nil, nil
	}
	out := make([]gleann.MetadataFilter, 0, len(vals))
	for _, v := range vals {
		f, err := parseCLIFilter(v)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	if err := gleann.ValidateMetadataFilters(out); err != nil {
		return nil, err
	}
	return out, nil
}
