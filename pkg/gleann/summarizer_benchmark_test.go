package gleann

import (
	"testing"
)

func BenchmarkExtractSummary(b *testing.B) {
	input := `
# Gleann Server
![Build Status](https://badge)

Inspired by the academic excellence of the Leann RAG backend, engineered for daily terminal use.
Gleann provides a robust semantic search interface over your code and documents.

## Features
- [x] TUI (Terminal User Interface)
- [x] Code Graph mapping via AST

Run the following command:
` + "```" + `bash
gleann start
` + "```" + `
This tool completely changes how you search.

Company Confidential 2024. Company Confidential 2024. Company Confidential 2024.
Do not distribute. Do not distribute. Do not distribute.

Voice User Interface (VUI) module successfully integrated.
We achieved a completely functional Voice User Interface with 90% success rate on the test sets.
The module perfectly handles offline voice commands without internet access.

Company Confidential 2024.
`

	for i := 0; i < b.N; i++ {
		ExtractSummary(input)
	}
}
