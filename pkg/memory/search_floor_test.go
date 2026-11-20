package memory

import "testing"

// Fails on the old code: one shared common word ("service") was enough, so
// every search below returned the block.
func TestSearchFloorRejectsWeakMatches(t *testing.T) {
	m := searchFixture(t,
		Block{Content: "Restart the billing service after rotating the database credentials"},
		Block{Content: "Prefer table driven tests for parsers"},
	)
	got, err := m.Search("how do I configure the nginx service for production traffic")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("weak single-word match returned: %v", contents(got))
	}
	got, err = m.SearchScoped("", "how do I configure the nginx service for production traffic")
	if err != nil || len(got) != 0 {
		t.Fatalf("SearchScoped = %v, %v; want nothing", contents(got), err)
	}
	got, err = m.Store().Search("how do I configure the nginx service for production traffic")
	if err != nil || len(got) != 0 {
		t.Fatalf("Store.Search = %v, %v; want nothing", contents(got), err)
	}
}

func TestSearchFloorKeepsRealMatches(t *testing.T) {
	m := searchFixture(t,
		Block{Content: "Restart the billing service after rotating the database credentials"},
		Block{Content: "Prefer table driven tests for parsers"},
	)
	cases := []struct {
		query string
		want  int
	}{
		{"billing", 1},         // one-token query needs one token
		{"billing service", 1}, // two tokens
		{"how do I rotate the database credentials", 1},       // 2 of 4 topic tokens
		{"RESTART THE BILLING SERVICE", 1},                    // case-insensitive
		{"credentials for the nginx proxy and the cdn", 0},    // 1 of 5 topic tokens: below the floor
		{"tables parsers", 1},                                 // stemmed plural
		{"database credentials billing frobnicate wibble", 1}, // 3 of 5
	}
	for _, c := range cases {
		got, _ := m.Search(c.query)
		if len(got) != c.want {
			t.Errorf("%q = %v, want %d", c.query, contents(got), c.want)
		}
	}
}

// A verbatim phrase always passes, even when it is a single weak word group.
func TestSearchFloorVerbatimPhraseAlwaysPasses(t *testing.T) {
	m := searchFixture(t, Block{Content: "Run the deploy script from the release branch"})
	got, _ := m.Search("deploy script")
	if len(got) != 1 {
		t.Fatalf("phrase = %v", contents(got))
	}
}

func TestSearchFloorEnvOff(t *testing.T) {
	m := searchFixture(t, Block{Content: "Restart the billing service after rotating the database credentials"})
	q := "how do I configure the nginx service for production traffic"
	if got, _ := m.Search(q); len(got) != 0 {
		t.Fatalf("floor on: %v", contents(got))
	}
	for _, v := range []string{"off", "0", "false"} {
		t.Setenv("GLEANN_MEMORY_SEARCH_FLOOR", v)
		if got, _ := m.Search(q); len(got) != 1 {
			t.Fatalf("GLEANN_MEMORY_SEARCH_FLOOR=%s: %v, want legacy any-token match", v, contents(got))
		}
	}
}

func TestSearchFloorIgnoresGenericLabel(t *testing.T) {
	m := searchFixture(t, Block{Label: "user_memory", Content: "Deploys go out on Fridays"})
	// "user memory" appears only in the default label, not in the content.
	if got, _ := m.Search("user memory settings"); len(got) != 0 {
		t.Fatalf("generic label matched: %v", contents(got))
	}
}

func TestRequiredMatches(t *testing.T) {
	f := defaultFloor()
	for n, want := range map[int]int{0: 1, 1: 1, 2: 1, 3: 2, 4: 2, 9: 2} {
		if got := f.requiredMatches(n); got != want {
			t.Errorf("requiredMatches(%d) = %d, want %d", n, got, want)
		}
	}
}
