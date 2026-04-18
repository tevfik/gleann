package gleann

import "testing"

func TestParseReActStep(t *testing.T) {
	tests := []struct {
		name      string
		response  string
		wantThought string
		wantAction  string
		wantInput   string
	}{
		{
			name: "full step",
			response: `Thought: I need to search for information about Go.
Action: leann_search
Action Input: golang concurrency patterns`,
			wantThought: "I need to search for information about Go.",
			wantAction:  "leann_search",
			wantInput:   "golang concurrency patterns",
		},
		{
			name: "empty response",
			response: "",
			wantThought: "",
			wantAction:  "",
			wantInput:   "",
		},
		{
			name: "only thought",
			response: "Thought: thinking about it",
			wantThought: "thinking about it",
			wantAction:  "",
			wantInput:   "",
		},
		{
			name: "with extra whitespace",
			response: "Thought:    lots of space   \nAction:   search  \nAction Input:   query  ",
			wantThought: "lots of space",
			wantAction:  "search",
			wantInput:   "query",
		},
		{
			name: "with mixed content",
			response: `Some preamble text
Thought: need to check
Other text here
Action: read_full_document
Action Input: /docs/readme.md`,
			wantThought: "need to check",
			wantAction:  "read_full_document",
			wantInput:   "/docs/readme.md",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			step := parseReActStep(tt.response)
			if step.Thought != tt.wantThought {
				t.Errorf("Thought = %q, want %q", step.Thought, tt.wantThought)
			}
			if step.Action != tt.wantAction {
				t.Errorf("Action = %q, want %q", step.Action, tt.wantAction)
			}
			if step.ActionInput != tt.wantInput {
				t.Errorf("ActionInput = %q, want %q", step.ActionInput, tt.wantInput)
			}
		})
	}
}

func TestNewReActAgent(t *testing.T) {
	tools := []ReActTool{
		{Name: "search", Description: "search docs"},
		{Name: "read", Description: "read a document"},
	}

	// maxSteps <= 0 defaults to 5
	agent := NewReActAgent(nil, tools, 0)
	if agent.maxSteps != 5 {
		t.Errorf("maxSteps = %d, want 5 for zero input", agent.maxSteps)
	}

	agent = NewReActAgent(nil, tools, -1)
	if agent.maxSteps != 5 {
		t.Errorf("maxSteps = %d, want 5 for negative input", agent.maxSteps)
	}

	agent = NewReActAgent(nil, tools, 10)
	if agent.maxSteps != 10 {
		t.Errorf("maxSteps = %d, want 10", agent.maxSteps)
	}

	if len(agent.tools) != 2 {
		t.Errorf("tools count = %d, want 2", len(agent.tools))
	}
	if _, ok := agent.tools["search"]; !ok {
		t.Error("expected 'search' tool in map")
	}
}

func TestReActStepFields(t *testing.T) {
	step := ReActStep{
		Thought:     "test thought",
		Action:      "test_action",
		ActionInput: "test input",
		Observation: "test observation",
	}

	if step.Thought != "test thought" {
		t.Error("Thought field mismatch")
	}
	if step.Action != "test_action" {
		t.Error("Action field mismatch")
	}
	if step.ActionInput != "test input" {
		t.Error("ActionInput field mismatch")
	}
	if step.Observation != "test observation" {
		t.Error("Observation field mismatch")
	}
}
