//go:build cgo && faiss

package main

// Register FAISS backend when built with -tags faiss.
import _ "github.com/tevfik/gleann/internal/backend/faiss"
