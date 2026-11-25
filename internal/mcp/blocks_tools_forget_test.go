package mcp

import (
	"context"
	"strings"
	"testing"

	mcpsdk "github.com/mark3labs/mcp-go/mcp"
)

func mcpText(res *mcpsdk.CallToolResult) string {
	if len(res.Content) == 0 {
		return ""
	}
	tc, _ := res.Content[0].(mcpsdk.TextContent)
	return tc.Text
}

// memory_forget: several content matches are refused and listed; "all": true
// deletes them.
func TestMemoryForgetMultiMatchNeedsAll(t *testing.T) {
	s := testMCPServer()
	defer s.Close()
	mgr, err := s.blockMem.get()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []string{"zqxjv forget marker one", "zqxjv forget marker two"} {
		if _, err := mgr.AddNote("long", "n", c); err != nil {
			t.Fatal(err)
		}
	}

	res, err := s.handleMemoryForget(context.Background(), makeCallToolReq(map[string]any{"id_or_query": "zqxjv"}))
	if err != nil || !res.IsError {
		t.Fatalf("multi-match forget = %+v, %v; want a tool error", res, err)
	}
	if txt := mcpText(res); !strings.Contains(txt, "marker one") || !strings.Contains(txt, "marker two") || !strings.Contains(txt, "all") {
		t.Errorf("error should list the matches and mention all: %s", txt)
	}
	if blocks, _ := mgr.Search("zqxjv"); len(blocks) != 2 {
		t.Fatalf("refused forget deleted blocks: %d left", len(blocks))
	}

	res, err = s.handleMemoryForget(context.Background(), makeCallToolReq(map[string]any{"id_or_query": "zqxjv", "all": true}))
	if err != nil || res.IsError || !strings.Contains(mcpText(res), "Forgot 2 block(s)") {
		t.Fatalf("all=true = %+v, %v", res, err)
	}
}
