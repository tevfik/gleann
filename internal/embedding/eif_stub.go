//go:build !eif

package embedding

import (
	"context"
	"fmt"
)

func (c *Computer) computeEIF(ctx context.Context, texts []string) ([][]float32, error) {
	return nil, fmt.Errorf("eif-runtime embeddings are not enabled in this build. Compile with -tags eif")
}

func (c *Computer) getEIFDim() int {
	return 0
}
