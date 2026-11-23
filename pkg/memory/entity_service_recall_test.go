package memory

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/tevfik/gleann/pkg/gleann"
)

// recallTestService opens a fresh store in a temp dir and seeds one namespace
// with a small graph: AuthService -[DEPENDS_ON]-> JWTValidator -[USES]-> Crypto,
// plus an unrelated pair and a second namespace for isolation checks.
func recallTestService(t *testing.T) *EntityService {
	t.Helper()
	store, err := OpenStore(filepath.Join(t.TempDir(), "memory.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	es := store.EntityService("proj", nil)
	payload := gleann.GraphInjectionPayload{
		Nodes: []gleann.MemoryGraphNode{
			{ID: "AuthService", Type: "service", Content: "Authenticates users"},
			{ID: "JWTValidator", Type: "component", Content: "Validates RS256 JWT signatures"},
			{ID: "Crypto", Type: "library", Content: "Signing primitives"},
			{ID: "Unrelated", Type: "misc", Content: "Nothing to see"},
			{ID: "Other", Type: "misc", Content: "Elsewhere"},
		},
		Edges: []gleann.MemoryGraphEdge{
			{From: "AuthService", To: "JWTValidator", RelationType: "DEPENDS_ON", Weight: 0.9},
			{From: "JWTValidator", To: "Crypto", RelationType: "USES", Weight: 1},
			{From: "Unrelated", To: "Other", RelationType: "LINKS", Weight: 1},
		},
	}
	if err := es.InjectEntities(context.Background(), payload); err != nil {
		t.Fatal(err)
	}

	// Same entity ID in another namespace must stay invisible.
	other := store.EntityService("other-ns", nil)
	if err := other.InjectEntities(context.Background(), gleann.GraphInjectionPayload{
		Nodes: []gleann.MemoryGraphNode{{ID: "AuthService", Type: "service", Content: "Other namespace auth"}},
	}); err != nil {
		t.Fatal(err)
	}
	return es
}

func TestSearchEntities_Matching(t *testing.T) {
	es := recallTestService(t)
	ctx := context.Background()

	tests := []struct {
		name  string
		query string
		limit int
		want  []string
	}{
		{"by id substring", "auth", 10, []string{"AuthService"}},
		{"case-insensitive content", "rs256 SIGNATURES", 10, []string{"JWTValidator"}},
		{"by type", "component", 10, []string{"JWTValidator"}},
		{"multi-token AND across fields", "jwt validates", 10, []string{"JWTValidator"}},
		{"multi-token AND no match", "auth crypto", 10, nil},
		{"no match", "zzz", 10, nil},
		{"empty query", "   ", 10, nil},
		{"zero limit", "auth", 0, nil},
		{"limit caps results", "e", 2, []string{"AuthService", "Crypto"}}, // ORDER BY id
		{"like metacharacters literal", "100%", 10, nil},
		{"underscore literal", "a_b", 10, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := es.SearchEntities(ctx, tc.query, tc.limit)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %d nodes (%+v), want %d", len(got), got, len(tc.want))
			}
			for i, id := range tc.want {
				if got[i].ID != id {
					t.Errorf("node[%d] = %q, want %q", i, got[i].ID, id)
				}
			}
		})
	}

	// Namespace isolation: only proj entities come back.
	got, err := es.SearchEntities(ctx, "auth", 10)
	if err != nil || len(got) != 1 || got[0].Content != "Authenticates users" {
		t.Errorf("namespace leak or wrong content: %+v (%v)", got, err)
	}
}

func TestSearchRelations_Matching(t *testing.T) {
	es := recallTestService(t)
	ctx := context.Background()

	got, err := es.SearchRelations(ctx, "depends_on", 10)
	if err != nil || len(got) != 1 || got[0].From != "AuthService" || got[0].To != "JWTValidator" || got[0].Weight != 0.9 {
		t.Fatalf("relation type match: %+v (%v)", got, err)
	}
	if got, err := es.SearchRelations(ctx, "zzz", 10); err != nil || len(got) != 0 {
		t.Errorf("no match: %+v (%v)", got, err)
	}
	if got, err := es.SearchRelations(ctx, "", 10); err != nil || len(got) != 0 {
		t.Errorf("empty query: %+v (%v)", got, err)
	}
	if got, err := es.SearchRelations(ctx, "s", 1); err != nil || len(got) != 1 {
		t.Errorf("limit: %+v (%v)", got, err)
	}
}

func TestEntityRelations_IncomingOutgoingAndFilter(t *testing.T) {
	es := recallTestService(t)
	ctx := context.Background()

	// Both directions: JWTValidator has one incoming and one outgoing edge.
	got, err := es.EntityRelations(ctx, []string{"JWTValidator"}, nil, 10)
	if err != nil || len(got) != 2 {
		t.Fatalf("incident edges = %+v (%v), want 2", got, err)
	}
	if got[0].From != "AuthService" || got[1].From != "JWTValidator" {
		t.Errorf("order = %+v, want src,dst,relation_type order", got)
	}

	// Relation-type filter is case-insensitive exact match.
	got, err = es.EntityRelations(ctx, []string{"JWTValidator"}, []string{"uses"}, 10)
	if err != nil || len(got) != 1 || got[0].RelationType != "USES" {
		t.Fatalf("filter: %+v (%v)", got, err)
	}
	if got, err := es.EntityRelations(ctx, []string{"JWTValidator"}, []string{"USE"}, 10); err != nil || len(got) != 0 {
		t.Errorf("partial type must not match: %+v (%v)", got, err)
	}

	// Multiple ids, deduplicated by the primary key.
	got, err = es.EntityRelations(ctx, []string{"AuthService", "JWTValidator", "AuthService"}, nil, 10)
	if err != nil || len(got) != 2 {
		t.Fatalf("multi-id: %+v (%v)", got, err)
	}

	// Budget and empty inputs.
	if got, err := es.EntityRelations(ctx, []string{"JWTValidator"}, nil, 1); err != nil || len(got) != 1 {
		t.Errorf("limit: %+v (%v)", got, err)
	}
	if got, err := es.EntityRelations(ctx, nil, nil, 10); err != nil || got != nil {
		t.Errorf("empty ids: %+v (%v)", got, err)
	}
	if got, err := es.EntityRelations(ctx, []string{"JWTValidator"}, nil, 0); err != nil || got != nil {
		t.Errorf("zero limit: %+v (%v)", got, err)
	}
	if got, err := es.EntityRelations(ctx, []string{"NoSuchNode"}, nil, 10); err != nil || len(got) != 0 {
		t.Errorf("unknown id: %+v (%v)", got, err)
	}
}

func TestEntitiesByIDs(t *testing.T) {
	es := recallTestService(t)
	ctx := context.Background()

	got, err := es.EntitiesByIDs(ctx, []string{"JWTValidator", "Missing", "AuthService", "JWTValidator"})
	if err != nil || len(got) != 2 {
		t.Fatalf("got %+v (%v), want 2", got, err)
	}
	if got[0].ID != "AuthService" || got[1].ID != "JWTValidator" {
		t.Errorf("order = %+v, want ORDER BY id", got)
	}
	if got[1].Type != "component" || got[1].Content != "Validates RS256 JWT signatures" {
		t.Errorf("fields lost: %+v", got[1])
	}
	if got, err := es.EntitiesByIDs(ctx, nil); err != nil || got != nil {
		t.Errorf("empty ids: %+v (%v)", got, err)
	}
	// Namespace isolation.
	if got, err := es.EntitiesByIDs(ctx, []string{"Other"}); err != nil || len(got) != 1 {
		t.Errorf("proj sees its own Other: %+v (%v)", got, err)
	}
}

func TestLikeContainsPattern_Escaping(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"abc", "%abc%"},
		{"ABC", "%abc%"},
		{"100%", `%100\%%`},
		{"a_b", `%a\_b%`},
		{`a\b`, `%a\\b%`},
	} {
		if got := likeContainsPattern(tc.in); got != tc.want {
			t.Errorf("likeContainsPattern(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// unicodeRecallService seeds a namespace with upper-case Turkish/German text so
// Unicode folding can be exercised end to end.
func unicodeRecallService(t *testing.T) *EntityService {
	t.Helper()
	store, err := OpenStore(filepath.Join(t.TempDir(), "memory.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	es := store.EntityService("proj", nil)
	if err := es.InjectEntities(context.Background(), gleann.GraphInjectionPayload{
		Nodes: []gleann.MemoryGraphNode{
			{ID: "Alpha", Type: "entity", Content: "alpha service"},
			{ID: "Beta", Type: "entity", Content: "beta service"},
			{ID: "ÇAĞRI", Type: "entity", Content: "çağrı merkezi"},
			{ID: "ÄRGER", Type: "entity", Content: "ÜBER ALLES"},
		},
		Edges: []gleann.MemoryGraphEdge{
			{From: "Alpha", To: "Beta", RelationType: "ÇAĞRI", Weight: 1},
			{From: "ÇAĞRI", To: "Beta", RelationType: "LINKS", Weight: 1},
		},
	}); err != nil {
		t.Fatal(err)
	}
	return es
}

// TestSearchEntities_NonASCIIMatching is finding 3: a Unicode-aware query must
// match stored upper-case text, including the exact stored value.
func TestSearchEntities_NonASCIIMatching(t *testing.T) {
	es := unicodeRecallService(t)
	ctx := context.Background()

	for _, tc := range []struct {
		query string
		want  string
	}{
		{"ärger", "ÄRGER"},
		{"ÄRGER", "ÄRGER"},
		{"über", "ÄRGER"},
		{"ÜBER", "ÄRGER"},
		{"rger", "ÄRGER"}, // ASCII substring inside a non-ASCII stored value
		{"çağrı", "ÇAĞRI"},
		{"ÇAĞRI", "ÇAĞRI"},
	} {
		got, err := es.SearchEntities(ctx, tc.query, 10)
		if err != nil {
			t.Fatalf("SearchEntities(%q): %v", tc.query, err)
		}
		if len(got) != 1 || got[0].ID != tc.want {
			t.Errorf("SearchEntities(%q) = %+v, want %s", tc.query, got, tc.want)
		}
	}
}

// TestSearchRelations_NonASCIIMatching is finding 3 for relation-type queries.
func TestSearchRelations_NonASCIIMatching(t *testing.T) {
	es := unicodeRecallService(t)
	ctx := context.Background()

	for _, query := range []string{"çağrı", "ÇAĞRI"} {
		got, err := es.SearchRelations(ctx, query, 10)
		if err != nil {
			t.Fatalf("SearchRelations(%q): %v", query, err)
		}
		if len(got) != 1 || got[0].From != "Alpha" || got[0].To != "Beta" || got[0].RelationType != "ÇAĞRI" {
			t.Errorf("SearchRelations(%q) = %+v", query, got)
		}
	}
}

// TestEntityRelations_NonASCIIFilter is finding 3 for the hop filter: both the
// stored spelling and a lower-case spelling must keep the edge.
func TestEntityRelations_NonASCIIFilter(t *testing.T) {
	es := unicodeRecallService(t)
	ctx := context.Background()

	for _, types := range [][]string{{"ÇAĞRI"}, {"çağrı"}} {
		got, err := es.EntityRelations(ctx, []string{"Alpha"}, types, 10)
		if err != nil {
			t.Fatalf("EntityRelations(%v): %v", types, err)
		}
		if len(got) != 1 || got[0].RelationType != "ÇAĞRI" {
			t.Errorf("EntityRelations(%v) = %+v, want the ÇAĞRI edge", types, got)
		}
	}
}

// TestSearchEntities_BoundsTokenCount is finding 5: unbounded token lists must
// not produce a SQLite "expression tree is too large" error.
func TestSearchEntities_BoundsTokenCount(t *testing.T) {
	es := recallTestService(t)
	var b strings.Builder
	for i := 0; i < 2000; i++ {
		b.WriteString("t")
		b.WriteString(strconv.Itoa(i))
		b.WriteByte(' ')
	}
	if _, err := es.SearchEntities(context.Background(), b.String(), 10); err != nil {
		t.Fatalf("SearchEntities with 2000 tokens: %v", err)
	}
}

// TestEntityRelations_BoundsRelationTypes is finding 5: a huge relation filter
// must be capped instead of exceeding SQLite's bind-parameter limit.
func TestEntityRelations_BoundsRelationTypes(t *testing.T) {
	es := recallTestService(t)
	types := make([]string, 40000)
	for i := range types {
		types[i] = "T" + strconv.Itoa(i)
	}
	got, err := es.EntityRelations(context.Background(), []string{"AuthService"}, types, 10)
	if err != nil {
		t.Fatalf("EntityRelations with 40000 filter entries: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %+v, want no matching relation types", got)
	}
}

// TestEntityRelations_EmptyRelationFilterIgnored is finding 7 at the store
// layer: empty/whitespace entries must behave like an omitted filter.
func TestEntityRelations_EmptyRelationFilterIgnored(t *testing.T) {
	es := recallTestService(t)
	ctx := context.Background()

	for _, types := range [][]string{{""}, {"   "}, {"", "  "}, {"", "depends_on"}} {
		got, err := es.EntityRelations(ctx, []string{"AuthService"}, types, 10)
		if err != nil {
			t.Fatalf("EntityRelations(%v): %v", types, err)
		}
		if len(got) != 1 || got[0].RelationType != "DEPENDS_ON" {
			t.Errorf("EntityRelations(%v) = %+v, want the DEPENDS_ON edge", types, got)
		}
	}
}

// turkishContentRecallService seeds entities/relations whose stored text uses
// native Turkish characters while the queries below are ASCII-only.
func turkishContentRecallService(t *testing.T) *EntityService {
	t.Helper()
	store, err := OpenStore(filepath.Join(t.TempDir(), "memory.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	es := store.EntityService("proj", nil)
	if err := es.InjectEntities(context.Background(), gleann.GraphInjectionPayload{
		Nodes: []gleann.MemoryGraphNode{
			{ID: "n1", Type: "entity", Content: "İstanbul büyükşehir"},
			{ID: "n2", Type: "entity", Content: "Diyarbakır karpuzu"},
		},
		Edges: []gleann.MemoryGraphEdge{
			{From: "n1", To: "n2", RelationType: "İstanbul", Weight: 1},
			{From: "n2", To: "n1", RelationType: "Diyarbakır", Weight: 1},
		},
	}); err != nil {
		t.Fatal(err)
	}
	return es
}

// TestSearchEntities_ASCIIQueryMatchesUnicodeContent is v3 finding 1: an
// ASCII query must match stored Unicode content, because FoldCase folds
// "İstanbul" -> "istanbul" and "Diyarbakır" -> "diyarbakir".
func TestSearchEntities_ASCIIQueryMatchesUnicodeContent(t *testing.T) {
	es := turkishContentRecallService(t)
	ctx := context.Background()

	for _, tc := range []struct {
		query string
		want  string
	}{
		{"istanbul", "n1"},
		{"İstanbul", "n1"},
		{"DIYARBAKIR", "n2"},
		{"diyarbakir", "n2"},
		{"Diyarbakır", "n2"},
	} {
		got, err := es.SearchEntities(ctx, tc.query, 10)
		if err != nil {
			t.Fatalf("SearchEntities(%q): %v", tc.query, err)
		}
		if len(got) != 1 || got[0].ID != tc.want {
			t.Errorf("SearchEntities(%q) = %+v, want node %s", tc.query, got, tc.want)
		}
	}
}

// TestSearchRelations_ASCIIQueryMatchesUnicodeContent is v3 finding 1 for
// relation-type queries: an ASCII query must match a stored "İstanbul" or
// "Diyarbakır" relation type.
func TestSearchRelations_ASCIIQueryMatchesUnicodeContent(t *testing.T) {
	es := turkishContentRecallService(t)
	ctx := context.Background()

	for _, tc := range []struct {
		query string
		want  string
	}{
		{"istanbul", "İstanbul"},
		{"DIYARBAKIR", "Diyarbakır"},
		{"diyarbakir", "Diyarbakır"},
	} {
		got, err := es.SearchRelations(ctx, tc.query, 10)
		if err != nil {
			t.Fatalf("SearchRelations(%q): %v", tc.query, err)
		}
		if len(got) != 1 || got[0].RelationType != tc.want {
			t.Errorf("SearchRelations(%q) = %+v, want relation %q", tc.query, got, tc.want)
		}
	}
}

// TestEntityRelations_ASCIIFilterMatchesUnicodeType is v3 finding 1 for the hop
// filter: an ASCII filter must match a stored Unicode relation type.
func TestEntityRelations_ASCIIFilterMatchesUnicodeType(t *testing.T) {
	es := turkishContentRecallService(t)
	ctx := context.Background()

	for _, tc := range []struct {
		id    string
		types []string
		want  string
	}{
		{"n1", []string{"istanbul"}, "İstanbul"},
		{"n2", []string{"DIYARBAKIR"}, "Diyarbakır"},
		{"n1", []string{"diyarbakir"}, "Diyarbakır"},
	} {
		got, err := es.EntityRelations(ctx, []string{tc.id}, tc.types, 10)
		if err != nil {
			t.Fatalf("EntityRelations(%s, %v): %v", tc.id, tc.types, err)
		}
		if len(got) != 1 || got[0].RelationType != tc.want {
			t.Errorf("EntityRelations(%s, %v) = %+v, want relation %q", tc.id, tc.types, got, tc.want)
		}
	}
}

// TestNormalizeRelationTypesTurkishCaseVariants is v3 finding 3: case variants
// that FoldCase collapses must deduplicate (ÇAĞRI vs çağrı).
func TestNormalizeRelationTypesTurkishCaseVariants(t *testing.T) {
	got := NormalizeRelationTypes([]string{"ÇAĞRI", "çağrı"})
	if len(got) != 1 {
		t.Fatalf("NormalizeRelationTypes([ÇAĞRI çağrı]) = %v (len %d), want len 1", got, len(got))
	}
	if got[0] != "ÇAĞRI" {
		t.Errorf("NormalizeRelationTypes kept %q, want first spelling %q", got[0], "ÇAĞRI")
	}
	if FoldCase(got[0]) != FoldCase("çağrı") {
		t.Errorf("dedup key not FoldCase-compatible: %q vs %q", got[0], "çağrı")
	}
}
