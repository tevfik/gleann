// Package gleann provides a ReAct (Reasoning + Acting) agent for multi-step Q&A.
package gleann

import (
	"context"
	"fmt"
	"strings"
)

// ReActTool represents a tool the agent can use.
type ReActTool struct {
	Name        string
	Description string
	Execute     func(ctx context.Context, input string) (string, error)
}

// ReActStep represents a single step in the ReAct loop.
type ReActStep struct {
	Thought     string `json:"thought"`
	Action      string `json:"action"`
	ActionInput string `json:"action_input"`
	Observation string `json:"observation"`
}

// ReActAgent performs multi-step reasoning using Thought-Action-Observation loops.
type ReActAgent struct {
	chat     *LeannChat
	tools    map[string]ReActTool
	maxSteps int
}

// NewReActAgent creates a new ReAct agent.
func NewReActAgent(chat *LeannChat, tools []ReActTool, maxSteps int) *ReActAgent {
	if maxSteps <= 0 {
		maxSteps = 5
	}

	toolMap := make(map[string]ReActTool, len(tools))
	for _, t := range tools {
		toolMap[t.Name] = t
	}

	return &ReActAgent{
		chat:     chat,
		tools:    toolMap,
		maxSteps: maxSteps,
	}
}

// Run executes the ReAct loop for a given question.
func (a *ReActAgent) Run(ctx context.Context, question string) (string, []ReActStep, error) {
	// Build tool descriptions.
	var toolDescs []string
	for _, t := range a.tools {
		toolDescs = append(toolDescs, fmt.Sprintf("- %s: %s", t.Name, t.Description))
	}

	systemPrompt := fmt.Sprintf(`You are a ReAct agent. You solve problems step by step.

Available tools:
%s

For each step, respond EXACTLY in this format:
Thought: [your reasoning]
Action: [tool name]
Action Input: [input for the tool]

When you have the final answer, respond with:
Thought: I now have the answer.
Final Answer: [your answer]

Do NOT add any other text.`, strings.Join(toolDescs, "\n"))

	a.chat.config.SystemPrompt = systemPrompt

	var steps []ReActStep
	conversationContext := fmt.Sprintf("Question: %s\n", question)

	for i := 0; i < a.maxSteps; i++ {
		// Ask the LLM for next step.
		messages := []ChatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: conversationContext},
		}

		response, err := a.chat.chat(ctx, messages)
		if err != nil {
			return "", steps, fmt.Errorf("step %d: %w", i+1, err)
		}

		// Parse response.
		response = strings.TrimSpace(response)

		// Check for Final Answer.
		if idx := strings.Index(response, "Final Answer:"); idx >= 0 {
			answer := strings.TrimSpace(response[idx+len("Final Answer:"):])
			return answer, steps, nil
		}

		// Parse Thought/Action/ActionInput.
		step := parseReActStep(response)

		// Execute tool.
		if tool, ok := a.tools[step.Action]; ok {
			observation, err := tool.Execute(ctx, step.ActionInput)
			if err != nil {
				step.Observation = fmt.Sprintf("Error: %v", err)
			} else {
				step.Observation = observation
			}
		} else if step.Action != "" {
			step.Observation = fmt.Sprintf("Unknown tool: %s", step.Action)
		}

		steps = append(steps, step)

		// Add to conversation context.
		conversationContext += fmt.Sprintf("\nThought: %s\nAction: %s\nAction Input: %s\nObservation: %s\n",
			step.Thought, step.Action, step.ActionInput, step.Observation)
	}

	return "I was unable to determine an answer within the maximum number of steps.", steps, nil
}

// NewSearchTool creates a ReActTool that searches a LEANN index.
func NewSearchTool(searcher *LeannSearcher) ReActTool {
	return ReActTool{
		Name:        "leann_search",
		Description: "Search the indexed documents for relevant information. Input: search query string.",
		Execute: func(ctx context.Context, input string) (string, error) {
			results, err := searcher.Search(ctx, input, WithTopK(5))
			if err != nil {
				return "", err
			}
			var parts []string
			for i, r := range results {
				source := ""
				if s, ok := r.Metadata["source"]; ok {
					source = fmt.Sprintf(" [%v]", s)
				}
				text := r.Text
				if len(text) > 300 {
					text = text[:300] + "..."
				}
				parts = append(parts, fmt.Sprintf("[%d]%s (score: %.3f) %s", i+1, source, r.Score, text))
			}
			if len(parts) == 0 {
				return "No results found.", nil
			}
			return strings.Join(parts, "\n"), nil
		},
	}
}

// parseReActStep parses a ReAct response into a Step.
func parseReActStep(response string) ReActStep {
	step := ReActStep{}
	lines := strings.Split(response, "\n")

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Thought:") {
			step.Thought = strings.TrimSpace(strings.TrimPrefix(line, "Thought:"))
		} else if strings.HasPrefix(line, "Action:") {
			step.Action = strings.TrimSpace(strings.TrimPrefix(line, "Action:"))
		} else if strings.HasPrefix(line, "Action Input:") {
			step.ActionInput = strings.TrimSpace(strings.TrimPrefix(line, "Action Input:"))
		}
	}

	return step
}
