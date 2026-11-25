package main

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode"

	"github.com/tevfik/gleann/pkg/gleann"
)

// parseCLIFilter turns a single --filter expression into a MetadataFilter.
//
// Grammar: "field op value". The field and operator are the first two
// whitespace-separated words; the value is everything after the operator,
// verbatim (internal whitespace is preserved). Typing rules, chosen so that
// the CLI never rewrites what the user typed:
//
//   - gt, gte, lt, lte: the value must be a number (sent as a JSON number).
//   - in, nin: the value must be a JSON array, e.g. ["go","api"].
//   - exists: the value may be omitted (field is present), or be true or
//     false; "lang exists false" keeps passages WITHOUT the field. This is
//     the same boolean the REST and MCP "exists" filter takes.
//     The derived fields ext/extension count an empty extension as absent.
//   - every other operator: the value is a string, verbatim, so
//     'version eq 1.10' compares "1.10". A value that starts with a double
//     quote is decoded as a JSON string, which is how to keep leading or
//     trailing spaces: 'title eq "a  b "'.
//
// Operators are case-sensitive, exactly like REST and MCP. A missing value
// is an error for every operator except exists.
func parseCLIFilter(expr string) (gleann.MetadataFilter, error) {
	usage := fmt.Errorf(
		"--filter %q: expected 'field op value' (operators: eq, ne, gt, gte, lt, lte, in, nin, contains, startswith, endswith, exists)",
		expr,
	)
	field, rest := cutWord(expr)
	op, rest := cutWord(rest)
	if field == "" || op == "" {
		return gleann.MetadataFilter{}, usage
	}
	f := gleann.MetadataFilter{Field: field, Operator: gleann.FilterOperator(op)}
	// Reuse the REST/MCP validator first so unknown operators fail the same way.
	probe := f
	probe.Value = []any{}
	if probe.Operator == gleann.OpExists {
		probe.Value = true // the value is parsed below; the operator check is what matters here
	}
	if err := probe.Validate(); err != nil {
		return gleann.MetadataFilter{}, fmt.Errorf("--filter %q: %w", expr, err)
	}
	raw := strings.TrimLeftFunc(rest, unicode.IsSpace)
	if !strings.HasPrefix(raw, `"`) {
		// Unquoted values lose their edge whitespace; quote to keep it.
		raw = strings.TrimRightFunc(raw, unicode.IsSpace)
	}
	if raw == "" {
		if f.Operator != gleann.OpExists {
			return gleann.MetadataFilter{}, fmt.Errorf("--filter %q: operator %q needs a value", expr, op)
		}
		f.Value = true
		return validatedCLIFilter(expr, f)
	}
	switch f.Operator {
	case gleann.OpGreaterThan, gleann.OpGreaterEqual, gleann.OpLessThan, gleann.OpLessEqual:
		n, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
		if err != nil || math.IsNaN(n) || math.IsInf(n, 0) {
			return gleann.MetadataFilter{}, fmt.Errorf("--filter %q: operator %q needs a number, got %q", expr, op, raw)
		}
		f.Value = n
	case gleann.OpIn, gleann.OpNotIn:
		var arr []any
		if err := json.Unmarshal([]byte(raw), &arr); err != nil {
			return gleann.MetadataFilter{}, fmt.Errorf("--filter %q: operator %q needs a JSON array such as [\"a\",\"b\"]", expr, op)
		}
		if arr == nil { // "null" decodes to a nil slice
			return gleann.MetadataFilter{}, fmt.Errorf("--filter %q: operator %q needs a non-null JSON array", expr, op)
		}
		f.Value = arr
	case gleann.OpExists:
		switch strings.TrimSpace(raw) {
		case "true":
			f.Value = true
		case "false":
			f.Value = false
		default:
			return gleann.MetadataFilter{}, fmt.Errorf("--filter %q: exists takes no value, true or false, got %q", expr, raw)
		}
	default:
		if strings.HasPrefix(raw, `"`) {
			var str string
			if err := json.Unmarshal([]byte(raw), &str); err != nil {
				return gleann.MetadataFilter{}, fmt.Errorf("--filter %q: bad quoted value: %v", expr, err)
			}
			f.Value = str
		} else {
			f.Value = raw
		}
	}
	return validatedCLIFilter(expr, f)
}

// validatedCLIFilter runs the shared REST/MCP validator on the parsed filter.
func validatedCLIFilter(expr string, f gleann.MetadataFilter) (gleann.MetadataFilter, error) {
	if err := f.Validate(); err != nil {
		return gleann.MetadataFilter{}, fmt.Errorf("--filter %q: %w", expr, err)
	}
	return f, nil
}

// cutWord splits off the first whitespace-delimited word of s.
func cutWord(s string) (word, rest string) {
	s = strings.TrimLeftFunc(s, unicode.IsSpace)
	i := strings.IndexFunc(s, unicode.IsSpace)
	if i < 0 {
		return s, ""
	}
	return s[:i], s[i:]
}

// cliFilterValues returns every --filter value in args, in order, and fails
// on a missing or empty one ("--filter" last, or "--filter="). Nothing after
// a literal "--" is a flag.
func cliFilterValues(args []string) ([]string, error) {
	var out []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			break
		}
		if a == "--filter" {
			if i+1 >= len(args) {
				return nil, fmt.Errorf("--filter needs an expression ('field op value')")
			}
			out = append(out, args[i+1])
			i++
			continue
		}
		if v, ok := strings.CutPrefix(a, "--filter="); ok {
			if v == "" {
				return nil, fmt.Errorf("--filter= is empty (expected 'field op value')")
			}
			out = append(out, v)
		}
	}
	return out, nil
}

// parseCLIFilters parses every repeatable --filter flag in args into
// validated metadata filters. It returns nil (no filters) when none are
// given, so callers can append the option unconditionally only when the
// slice is non-empty.
func parseCLIFilters(args []string) ([]gleann.MetadataFilter, error) {
	vals, err := cliFilterValues(args)
	if err != nil {
		return nil, err
	}
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
