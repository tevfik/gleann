package gleann

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMetadataFilterRejectsUnknownKey(t *testing.T) {
	var f []MetadataFilter
	err := json.Unmarshal([]byte(`[{"field":"kind","op":"eq","value":"x"}]`), &f)
	if err == nil || !strings.Contains(err.Error(), "op") {
		t.Fatalf("want an error naming the unknown key, got %v", err)
	}
	if err := json.Unmarshal([]byte(`[{"field":"kind","operator":"eq","value":"x"}]`), &f); err != nil {
		t.Fatal(err)
	}
	if f[0].Operator != OpEqual {
		t.Fatalf("operator %q", f[0].Operator)
	}
}

func TestValidateMetadataFilters(t *testing.T) {
	if err := ValidateMetadataFilters([]MetadataFilter{{Field: "a", Operator: OpExists}}); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []MetadataFilter{{Field: "a"}, {Field: "a", Operator: "bogus"}, {Operator: OpEqual}} {
		if ValidateMetadataFilters([]MetadataFilter{bad}) == nil {
			t.Errorf("%+v accepted", bad)
		}
	}
}
