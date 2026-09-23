package mcp

import (
	"path/filepath"
	"strings"
)

// isNoisePath returns true if the path points to vendored submodules, build artifacts,
// internal test fixtures, or external dependencies that crowd out core repository architecture.
func isNoisePath(path string) bool {
	lower := strings.ToLower(filepath.ToSlash(path))
	noisePatterns := []string{
		"/submodules/",
		"/submodule/",
		"/vendor/",
		"/third_party/",
		"/thirdparty/",
		"/external/",
		"/tensorflow_lite_micro/",
		"/pigweed/",
		"/flightgear_bridge/",
		"/node_modules/",
		"/test/",
		"/tests/",
		"/unittest/",
		"/unit_test/",
		"/testing/",
		"/mocks/",
		"/mock/",
		"/fixtures/",
		"/build/",
		"/dist/",
		"/.github/",
		"/examples/",
		"/sample/",
		"/samples/",
		"/nuttx/",
		"/rl_tools/",
		"/zenoh-pico/",
		"/libtomcrypt/",
		"/monocypher/",
		"/tools/",
		"/fuzztest/",
		"/centipede/",
		"/micro-xrce-dds-client/",
		"/gemmlowp/",
		"/ruy/",
		"/flatbuffers/",
		"/eigen/",
		"/pymavlink/",
		"/libdronecan/",
		"/dronecan/",
		"/googletest/",
	}
	for _, p := range noisePatterns {
		if strings.Contains(lower, p) || strings.HasPrefix(lower, strings.TrimPrefix(p, "/")) {
			return true
		}
	}
	return false
}

// isNoiseSymbol returns true if the symbol is a trivial boilerplate, compiler-generated block,
// or low-level primitive that adds noise to architectural maps.
func isNoiseSymbol(name, kind string) bool {
	if kind == "variable" || kind == "field" || kind == "parameter" || kind == "constant" || kind == "macro" || kind == "block" {
		return true
	}
	lower := strings.ToLower(name)
	if strings.HasPrefix(lower, "block_") || strings.HasPrefix(lower, "lambda_") || strings.HasPrefix(lower, "__") {
		return true
	}
	switch lower {
	case "trailing", "init", "main", "str", "get", "set", "size", "len", "free", "malloc", "memcpy", "memset", "operator()", "operator=", "operator==":
		return true
	}
	return false
}
