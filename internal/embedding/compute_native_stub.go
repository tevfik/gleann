//go:build !native

package embedding

import (
	"context"
	"fmt"
)

func (c *Computer) computeNative(ctx context.Context, texts []string) ([][]float32, error) {
	return nil, fmt.Errorf("native embeddings are not enabled in this build. Compile with -tags native")
}
