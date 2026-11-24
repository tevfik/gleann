package main

import (
	"bytes"
	"encoding/json"
	"io"
	"sort"
)

// jsonOrder mirrors the key order of a parsed JSON document: for an object,
// the keys in file order and the order of each child; for an array, the order
// of each element. Scalars have no entry (nil).
type jsonOrder struct {
	keys  []string
	kids  map[string]*jsonOrder
	elems []*jsonOrder
}

func parseJSONOrder(data []byte) *jsonOrder {
	dec := json.NewDecoder(bytes.NewReader(data))
	o, err := parseOrderValue(dec)
	if err != nil {
		return nil
	}
	return o
}

func parseOrderValue(dec *json.Decoder) (*jsonOrder, error) {
	t, err := dec.Token()
	if err != nil {
		return nil, err
	}
	d, ok := t.(json.Delim)
	if !ok {
		return nil, nil
	}
	o := &jsonOrder{kids: map[string]*jsonOrder{}}
	if d == '{' {
		for dec.More() {
			kt, err := dec.Token()
			if err != nil {
				return nil, err
			}
			k, _ := kt.(string)
			kid, err := parseOrderValue(dec)
			if err != nil {
				return nil, err
			}
			if _, dup := o.kids[k]; !dup {
				o.keys = append(o.keys, k)
			}
			o.kids[k] = kid
		}
	} else {
		for dec.More() {
			kid, err := parseOrderValue(dec)
			if err != nil {
				return nil, err
			}
			o.elems = append(o.elems, kid)
		}
	}
	if _, err := dec.Token(); err != nil && err != io.EOF {
		return nil, err
	}
	return o, nil
}

// marshalKeepingOrder renders v as indented JSON (two spaces, no HTML
// escaping) with object keys in the order they have in original, the
// previous content of the file (nil for a new file). Keys absent from the
// original follow, sorted. Re-encoding a decoded document this way changes
// nothing but the edit: `2>/dev/null` stays as written and the user's key
// order survives, which json.MarshalIndent of a map would not do.
func marshalKeepingOrder(original []byte, v interface{}) ([]byte, error) {
	var ord *jsonOrder
	if len(original) > 0 {
		ord = parseJSONOrder(original)
	}
	var buf bytes.Buffer
	if err := writeOrdered(&buf, v, ord); err != nil {
		return nil, err
	}
	var out bytes.Buffer
	if err := json.Indent(&out, buf.Bytes(), "", "  "); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func writeOrdered(buf *bytes.Buffer, v interface{}, ord *jsonOrder) error {
	switch x := v.(type) {
	case map[string]interface{}:
		keys := make([]string, 0, len(x))
		seen := map[string]bool{}
		if ord != nil {
			for _, k := range ord.keys {
				if _, ok := x[k]; ok {
					keys = append(keys, k)
					seen[k] = true
				}
			}
		}
		var rest []string
		for k := range x {
			if !seen[k] {
				rest = append(rest, k)
			}
		}
		sort.Strings(rest)
		keys = append(keys, rest...)
		buf.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := writeOrdered(buf, k, nil); err != nil {
				return err
			}
			buf.WriteByte(':')
			var kid *jsonOrder
			if ord != nil {
				kid = ord.kids[k]
			}
			if err := writeOrdered(buf, x[k], kid); err != nil {
				return err
			}
		}
		buf.WriteByte('}')
	case []interface{}:
		buf.WriteByte('[')
		for i, e := range x {
			if i > 0 {
				buf.WriteByte(',')
			}
			var kid *jsonOrder
			if ord != nil && i < len(ord.elems) {
				kid = ord.elems[i]
			}
			if err := writeOrdered(buf, e, kid); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
	default:
		var sb bytes.Buffer
		enc := json.NewEncoder(&sb)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(v); err != nil {
			return err
		}
		buf.Write(bytes.TrimRight(sb.Bytes(), "\n"))
	}
	return nil
}
