package graphstore

import "testing"

func TestInDegreeTopExcludingKinds(t *testing.T) {
	g, _ := openTestGraph(t)
	macro := mkSym("a.MACRO", "a.go", 1)
	macro.Kind = "macro"
	mustApply(t, g, FileDelta{
		File: "a.go", GraphHash: "ha",
		Symbols: []Symbol{macro, mkSym("a.Hub", "a.go", 10), mkSym("a.One", "a.go", 20), mkSym("a.Two", "a.go", 30)},
		Edges: []Edge{
			{Src: "a.One", Dst: "a.MACRO"}, {Src: "a.Two", Dst: "a.MACRO"}, {Src: "a.Hub", Dst: "a.MACRO"},
			{Src: "a.One", Dst: "a.Hub"}, {Src: "a.Two", Dst: "a.Hub"},
			{Src: "a.Hub", Dst: "a.Two"},
			{Src: "a.One", Dst: "ext.Std", DstExternal: true},
		},
	})
	all, err := g.InDegreeTopExcluding(10)
	if err != nil || len(all) != 3 || all[0].FQN != "a.MACRO" || all[0].InDegree != 3 {
		t.Fatalf("unfiltered = %+v, %v; want MACRO(3) first", all, err)
	}
	got, err := g.InDegreeTopExcluding(10, "macro")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].FQN != "a.Hub" || got[0].InDegree != 2 || got[1].FQN != "a.Two" || got[1].InDegree != 1 {
		t.Fatalf("excluding macro = %+v; want Hub(2), Two(1)", got)
	}
	one, err := g.InDegreeTopExcluding(1, "macro")
	if err != nil || len(one) != 1 || one[0].FQN != "a.Hub" {
		t.Fatalf("limit 1 = %+v, %v", one, err)
	}
}
