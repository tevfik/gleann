// Package kuzu is the Kùzu candidate adapter. It needs CGo and is compiled
// only with -tags kuzu (see kuzu.go); without the tag this package is empty so
// `go test ./...` works in a plain CGO_ENABLED=0 build.
package kuzu
