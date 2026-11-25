package server

import (
	"encoding/json"
	"testing"

	"github.com/tevfik/gleann/pkg/gleann"
)

// REST: {"field":"source","operator":"exists"} without value means true.
func TestSearchRequestExistsOmittedValueMeansTrue(t *testing.T) {
	for raw, want := range map[string]bool{
		`{"query":"q","metadata_filters":[{"field":"source","operator":"exists"}]}`:               true,
		`{"query":"q","metadata_filters":[{"field":"source","operator":"exists","value":true}]}`:  true,
		`{"query":"q","metadata_filters":[{"field":"source","operator":"exists","value":false}]}`: false,
	} {
		var req searchRequest
		if err := json.Unmarshal([]byte(raw), &req); err != nil {
			t.Fatal(err)
		}
		if err := gleann.ValidateMetadataFilters(req.MetadataFilters); err != nil {
			t.Fatalf("%s: %v", raw, err)
		}
		e := gleann.NewMetadataFilterEngine(req.MetadataFilters)
		if got := e.Match(map[string]any{"source": "a.go"}); got != want {
			t.Errorf("%s: passage with source matched=%v, want %v", raw, got, want)
		}
		if got := e.Match(map[string]any{}); got == want {
			t.Errorf("%s: passage without source matched=%v", raw, got)
		}
	}
}
