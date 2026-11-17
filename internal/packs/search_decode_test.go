package packs

import "testing"

func TestDecodeItemsShapes(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		want    int
		wantErr bool
	}{
		{"top-level list", "- id: a\n- id: b\n", 2, false},
		{"map with single list field", "meta: x\nitems:\n  - id: a\n  - id: b\n  - id: c\n", 3, false},
		{"map with several lists is one item", "a:\n  - 1\nb:\n  - 2\n", 1, false},
		{"plain map is one item", "id: solo\nname: n\n", 1, false},
		{"list with scalar", "- id: a\n- 5\n", 0, true},
		{"scalar document", "just a string\n", 0, true},
		{"invalid yaml", "a: [unclosed\n", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := decodeItems([]byte(tt.yaml))
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if len(got) != tt.want {
				t.Fatalf("got %d items, want %d", len(got), tt.want)
			}
		})
	}
}

func TestMatchItem(t *testing.T) {
	item := map[string]any{"title": "Hello World", "body": "secret", "n": 3}
	tests := []struct {
		name   string
		fields []string
		q      string
		want   bool
	}{
		{"configured field hit", []string{"title"}, "hello", true},
		{"configured field miss ignores other fields", []string{"title"}, "secret", false},
		{"missing field skipped", []string{"nope", "body"}, "secret", true},
		{"non-string field never matches", []string{"n"}, "3", false},
		{"no fields: any string field", nil, "secret", true},
		{"no fields: miss", nil, "absent", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := matchItem(item, tt.fields, tt.q); got != tt.want {
				t.Fatalf("matchItem = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRegistrySearchLimit(t *testing.T) {
	dir := t.TempDir()
	writePack(t, dir, "p", "id: p\nversion: \"1\"\ncontent_files: [items.yaml]\nsearch:\n  fields: [name]\n",
		"- name: Alpha\n- name: alpine\n- name: Beta\n")
	r := New(dir)
	if err := r.Reload(); err != nil {
		t.Fatal(err)
	}
	got, err := r.Search("p", "AL", 1)
	if err != nil || len(got) != 1 {
		t.Fatalf("limit 1: %v %v", got, err)
	}
	got, _ = r.Search("p", "al", 0)
	if len(got) != 2 {
		t.Fatalf("default limit, want 2 matches, got %d", len(got))
	}
	if _, err := r.Search("zzz", "x", 5); err != ErrNotFound {
		t.Fatalf("unknown pack: %v", err)
	}
	if _, err := r.Item("p", "nope"); err != ErrNotFound {
		t.Fatalf("unknown item: %v", err)
	}
}
