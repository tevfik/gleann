package a2a

import "context"

// scopeKey is the request-context key for the authenticated caller's token
// scope. It is unexported so only WithScope/ScopeFromContext can touch it.
type scopeKey struct{}

// WithScope returns a context carrying the caller's authenticated token scope
// (e.g. "query" or "admin"). The HTTP front end sets it after verifying the
// bearer token so skill handlers can authorise privileged operations.
func WithScope(ctx context.Context, scope string) context.Context {
	return context.WithValue(ctx, scopeKey{}, scope)
}

// ScopeFromContext returns the caller's token scope, or "" when no scope was
// attached. Handlers must treat an unknown scope as unprivileged (fail closed).
func ScopeFromContext(ctx context.Context) string {
	s, _ := ctx.Value(scopeKey{}).(string)
	return s
}
