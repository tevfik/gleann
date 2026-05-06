//go:build native

package embedding

import "context"

func (c *Computer) computeNative(ctx context.Context, texts []string) ([][]float32, error) {
	nativeComputer := NewNativeComputer(c.model)
	return nativeComputer.Compute(ctx, texts)
}
