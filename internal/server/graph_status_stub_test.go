//go:build !treesitter

package server

import "net/http"

// Without graph support every graph open fails with errGraphUnsupported.
const missingGraphIndexStatus = http.StatusServiceUnavailable
