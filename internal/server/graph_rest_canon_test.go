package server

import (
	"encoding/json"
	"sort"
	"testing"

	"github.com/tevfik/gleann/internal/graph/analysisgolden"
)

// canonicalREST decodes a response body, sorts the arrays that have no defined
// order and re-encodes it with the volatile values normalised.
func canonicalREST(t *testing.T, env *analysisgolden.Env, status int, body []byte) string {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatalf("response is not a JSON object: %v\n%s", err, body)
	}
	sortStrings := func(key string) {
		if arr, ok := v[key].([]any); ok {
			sort.Slice(arr, func(i, j int) bool { return arr[i].(string) < arr[j].(string) })
		}
	}
	for _, k := range []string{"direct_callers", "transitive_callers", "test_callers", "affected_files"} {
		sortStrings(k)
	}
	if arr, ok := v["results"].([]any); ok {
		sort.Slice(arr, func(i, j int) bool {
			return arr[i].(map[string]any)["fqn"].(string) < arr[j].(map[string]any)["fqn"].(string)
		})
	}
	out, err := json.Marshal(map[string]any{"status": status, "body": v})
	if err != nil {
		t.Fatal(err)
	}
	return env.CanonicalJSON(t, string(out))
}
