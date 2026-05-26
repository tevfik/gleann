// Package server — request body size limit middleware.
//
// Without an explicit limit, a malicious client can stream gigabytes
// into json.Decode and exhaust process memory. Most gleann endpoints
// accept compact JSON payloads; the default cap of 16 MiB is more than
// enough for build requests with thousands of small items.
//
// Override with GLEANN_MAX_BODY_BYTES (set to 0 to disable).
package server

import (
	"net/http"
	"os"
	"strconv"
	"sync"
)

const defaultMaxBodyBytes = 16 << 20 // 16 MiB

var (
	maxBodyOnce sync.Once
	maxBodyVal  int64
)

func maxBodyBytes() int64 {
	maxBodyOnce.Do(func() {
		maxBodyVal = defaultMaxBodyBytes
		if v := os.Getenv("GLEANN_MAX_BODY_BYTES"); v != "" {
			if n, err := strconv.ParseInt(v, 10, 64); err == nil {
				maxBodyVal = n
			}
		}
	})
	return maxBodyVal
}

// bodyLimitMiddleware enforces a per-request body cap for methods that
// typically carry payloads (POST, PUT, PATCH). GET/DELETE/OPTIONS pass
// through untouched.
func bodyLimitMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		limit := maxBodyBytes()
		if limit > 0 && r.Body != nil {
			switch r.Method {
			case http.MethodPost, http.MethodPut, http.MethodPatch:
				r.Body = http.MaxBytesReader(w, r.Body, limit)
			}
		}
		next.ServeHTTP(w, r)
	})
}
