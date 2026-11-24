package gleann

import "testing"

// The meta hook runs on the metadata BuildPassages writes, so governance
// fields land in the write that creates the index, and a rebuild keeps them
// when no hook overrides them.
func TestBuilderMetaHookCarriesGovernanceInFirstWrite(t *testing.T) {
	cfg := DefaultConfig()
	cfg.IndexDir = t.TempDir()
	b, err := NewBuilder(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	b.SetMetaHook(func(m *IndexMeta) { m.Tags = []string{"team"}; m.Description = "d" })
	if _, err := b.BuildPassages("idx", []Item{{Text: "hello"}}); err != nil {
		t.Fatal(err)
	}
	m, err := GetIndexMeta(cfg.IndexDir, "idx")
	if err != nil || len(m.Tags) != 1 || m.Tags[0] != "team" || m.Description != "d" {
		t.Fatalf("meta after the first write = %+v, %v", m, err)
	}

	b2, _ := NewBuilder(cfg, nil)
	if _, err := b2.BuildPassages("idx", []Item{{Text: "again"}}); err != nil {
		t.Fatal(err)
	}
	if m, _ := GetIndexMeta(cfg.IndexDir, "idx"); len(m.Tags) != 1 || m.Tags[0] != "team" {
		t.Errorf("a rebuild without a hook lost the tags: %+v", m)
	}
}

func TestHasTagToleratesLegacyAtTags(t *testing.T) {
	m := IndexMeta{Tags: []string{"@Team", " x "}}
	for _, tag := range []string{"team", "@team", "TEAM", "x"} {
		if !m.HasTag(tag) {
			t.Errorf("HasTag(%q) = false for stored %v", tag, m.Tags)
		}
	}
	if got := NormalizeTags([]string{"@Team", " ", " y "}); len(got) != 2 || got[0] != "Team" || got[1] != "y" {
		t.Errorf("NormalizeTags = %v", got)
	}
}
