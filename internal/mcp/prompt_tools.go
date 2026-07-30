package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/tevfik/gleann/pkg/gleann"
)

// registerPrompts adds the predefined prompt templates to the MCP server.
func (s *Server) registerPrompts() {
	// 1. Deep Refactor Prompt
	s.mcpServer.AddPrompt(mcp.Prompt{
		Name:        "gleann-deep-refactor",
		Description: "Generate a comprehensive refactoring plan for a specific component using Gleann's codebase knowledge.",
		Arguments: []mcp.PromptArgument{
			{
				Name:        "index",
				Description: "Name of the Gleann index to search (e.g. 'gleann-repo')",
				Required:    true,
			},
			{
				Name:        "component",
				Description: "The name of the component or module to refactor (e.g. 'chat system', 'embedding cache')",
				Required:    true,
			},
		},
	}, s.handleDeepRefactorPrompt)

	// 2. Bug Hunter Prompt
	s.mcpServer.AddPrompt(mcp.Prompt{
		Name:        "gleann-bug-hunter",
		Description: "Analyze a specific error or bug using Gleann's graph context and search capabilities.",
		Arguments: []mcp.PromptArgument{
			{
				Name:        "index",
				Description: "Name of the Gleann index to search",
				Required:    true,
			},
			{
				Name:        "error_description",
				Description: "Description of the error, log output, or stack trace",
				Required:    true,
			},
		},
	}, s.handleBugHunterPrompt)
}

func (s *Server) handleDeepRefactorPrompt(ctx context.Context, request mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	indexName, ok := request.Params.Arguments["index"]
	if !ok || indexName == "" {
		return nil, fmt.Errorf("index argument is required")
	}
	component, ok := request.Params.Arguments["component"]
	if !ok || component == "" {
		return nil, fmt.Errorf("component argument is required")
	}

	searcher, err := s.getSearcher(indexName)
	if err != nil {
		return nil, fmt.Errorf("failed to load index %q: %v", indexName, err)
	}

	// Retrieve context about the component
	query := fmt.Sprintf("architecture and implementation of %s", component)
	results, err := searcher.Search(ctx, query, gleann.WithTopK(10), gleann.WithGraphContext(true))
	if err != nil {
		return nil, fmt.Errorf("failed to search for component: %v", err)
	}

	var contextBuilder strings.Builder
	for _, r := range results {
		source, _ := r.Metadata["source"].(string)
		contextBuilder.WriteString(fmt.Sprintf("\n--- Source: %s ---\n%s\n", source, r.Text))
	}

	promptText := fmt.Sprintf(`You are an expert Software Architect.
The user wants to refactor the following component: "%s".

Below is the retrieved context from the codebase regarding this component:
%s

Please analyze this context and provide a step-by-step refactoring plan.
Consider SOLID principles, Clean Code, performance implications, and potential breaking changes.`, component, contextBuilder.String())

	return &mcp.GetPromptResult{
		Description: "Deep Refactor Plan",
		Messages: []mcp.PromptMessage{
			{
				Role: mcp.RoleUser,
				Content: mcp.TextContent{
					Type: "text",
					Text: promptText,
				},
			},
		},
	}, nil
}

func (s *Server) handleBugHunterPrompt(ctx context.Context, request mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	indexName, ok := request.Params.Arguments["index"]
	if !ok || indexName == "" {
		return nil, fmt.Errorf("index argument is required")
	}
	errorDesc, ok := request.Params.Arguments["error_description"]
	if !ok || errorDesc == "" {
		return nil, fmt.Errorf("error_description argument is required")
	}

	searcher, err := s.getSearcher(indexName)
	if err != nil {
		return nil, fmt.Errorf("failed to load index %q: %v", indexName, err)
	}

	// Retrieve context about the error
	results, err := searcher.Search(ctx, errorDesc, gleann.WithTopK(10), gleann.WithGraphContext(true))
	if err != nil {
		return nil, fmt.Errorf("failed to search for error context: %v", err)
	}

	var contextBuilder strings.Builder
	for _, r := range results {
		source, _ := r.Metadata["source"].(string)
		contextBuilder.WriteString(fmt.Sprintf("\n--- Source: %s ---\n%s\n", source, r.Text))
	}

	promptText := fmt.Sprintf(`You are an expert Debugging Assistant.
The user is encountering the following error/bug: 
"%s"

Below is the retrieved context from the codebase that might be related to this error:
%s

Please analyze this context and identify the potential root cause. 
Provide a detailed explanation of why the error occurs and suggest a code fix.`, errorDesc, contextBuilder.String())

	return &mcp.GetPromptResult{
		Description: "Bug Hunter Analysis",
		Messages: []mcp.PromptMessage{
			{
				Role: mcp.RoleUser,
				Content: mcp.TextContent{
					Type: "text",
					Text: promptText,
				},
			},
		},
	}, nil
}
