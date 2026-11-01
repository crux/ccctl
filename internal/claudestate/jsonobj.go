package claudestate

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// member is one key/value pair of a JSON object. The value keeps its
// original bytes, so numbers and strings are written back unchanged.
type member struct {
	Key   string
	Value json.RawMessage
}

// parseObject decodes a JSON object into its members in file order.
// Go maps lose the order; Claude Code's files keep it, and an edit by
// ccctl must not reorder the whole file.
func parseObject(b []byte) ([]member, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, fmt.Errorf("not a JSON object")
	}
	var out []member
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, ok := tok.(string)
		if !ok {
			return nil, fmt.Errorf("object key is not a string")
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, err
		}
		out = append(out, member{Key: key, Value: raw})
	}
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err == nil {
		return nil, fmt.Errorf("trailing data after JSON object")
	}
	return out, nil
}

// encodeObject writes members as one compact JSON object, the form
// JSON.stringify(x) produces.
func encodeObject(members []member) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, m := range members {
		if i > 0 {
			buf.WriteByte(',')
		}
		k, err := marshalString(m.Key)
		if err != nil {
			return nil, err
		}
		buf.Write(k)
		buf.WriteByte(':')
		buf.Write(m.Value)
	}
	buf.WriteByte('}')
	var out bytes.Buffer
	if err := json.Compact(&out, buf.Bytes()); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// encodeObjectIndented writes members in the form
// JSON.stringify(x, null, 2) produces.
func encodeObjectIndented(members []member) ([]byte, error) {
	compact, err := encodeObject(members)
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	if err := json.Indent(&out, compact, "", "  "); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// marshalString encodes s as a JSON string without Go's HTML escaping
// of <, > and &, which JSON.stringify does not do either.
func marshalString(s string) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(s); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}
