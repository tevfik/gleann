//go:build !eif

package gleann

import (
	"context"
	"fmt"
)

func (c *LeannChat) chatEIF(ctx context.Context, messages []ChatMessage) (string, error) {
	return "", fmt.Errorf("gleann was compiled without EIF runtime support; rebuild with -tags eif")
}

func (c *LeannChat) chatEIFStream(ctx context.Context, messages []ChatMessage, callback StreamCallback) error {
	return fmt.Errorf("gleann was compiled without EIF runtime support; rebuild with -tags eif")
}
