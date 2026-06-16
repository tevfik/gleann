//go:build treesitter

package indexer

import "encoding/json"

func jsonMarshal(v any) ([]byte, error)  { return json.Marshal(v) }
func jsonUnmarshal(data []byte, v any) error { return json.Unmarshal(data, v) }
