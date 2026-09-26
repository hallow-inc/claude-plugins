package decode

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"regexp"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

type Pos struct {
	Line   int
	Column int
}

type Doc struct {
	Value any
	Pos   map[string]Pos
}

type Error struct {
	Line   int
	Column int
	Msg    string
}

func (e *Error) Error() string {
	if e.Line == 0 {
		return e.Msg
	}
	return fmt.Sprintf("line %d, column %d: %s", e.Line, e.Column, e.Msg)
}

func errAt(n *yaml.Node, format string, args ...any) *Error {
	return &Error{Line: n.Line, Column: n.Column, Msg: fmt.Sprintf(format, args...)}
}

func YAML(data []byte) (Doc, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	var root yaml.Node
	if err := dec.Decode(&root); err != nil {
		if errors.Is(err, io.EOF) {
			return Doc{}, &Error{Msg: "empty document"}
		}
		return Doc{}, &Error{Msg: err.Error()}
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		if err != nil {
			return Doc{}, &Error{Msg: err.Error()}
		}
		return Doc{}, errAt(&extra, "file contains more than one YAML document")
	}
	w := walker{pos: map[string]Pos{}}
	v, err := w.node(root.Content[0], "")
	if err != nil {
		return Doc{}, err
	}
	return Doc{Value: v, Pos: w.pos}, nil
}

type walker struct {
	pos map[string]Pos
}

func (w *walker) node(n *yaml.Node, ptr string) (any, error) {
	if n.Kind == yaml.AliasNode || n.Anchor != "" {
		return nil, errAt(n, "anchors and aliases are not allowed: a reviewed file must mean what it says in place")
	}
	w.pos[ptr] = Pos{Line: n.Line, Column: n.Column}
	switch n.Kind {
	case yaml.MappingNode:
		return w.mapping(n, ptr)
	case yaml.SequenceNode:
		out := make([]any, 0, len(n.Content))
		for i, c := range n.Content {
			v, err := w.node(c, ptr+"/"+strconv.Itoa(i))
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		return out, nil
	case yaml.ScalarNode:
		return scalar(n)
	default:
		return nil, errAt(n, "unsupported YAML node")
	}
}

func (w *walker) mapping(n *yaml.Node, ptr string) (any, error) {
	out := make(map[string]any, len(n.Content)/2)
	seen := make(map[string]int, len(n.Content)/2)
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		if k.ShortTag() == "!!merge" {
			return nil, errAt(k, "merge keys (<<) are not allowed")
		}
		if k.Kind != yaml.ScalarNode {
			return nil, errAt(k, "mapping keys must be scalars")
		}
		if first, dup := seen[k.Value]; dup {
			return nil, errAt(k, "duplicate key %q (first at line %d)", k.Value, first)
		}
		seen[k.Value] = k.Line
		val, err := w.node(v, ptr+"/"+escape(k.Value))
		if err != nil {
			return nil, err
		}
		out[k.Value] = val
	}
	return out, nil
}

var decimalInt = regexp.MustCompile(`^[-+]?[0-9]+$`)

func scalar(n *yaml.Node) (any, error) {
	switch n.ShortTag() {
	case "!!str", "!!timestamp":
		return n.Value, nil
	case "!!null":
		return nil, nil
	case "!!bool":
		var b bool
		if err := n.Decode(&b); err != nil {
			return nil, errAt(n, "invalid boolean %q", n.Value)
		}
		return b, nil
	case "!!int":
		return integer(n)
	case "!!float":
		if decimalInt.MatchString(n.Value) {
			return integer(n)
		}
		f, err := strconv.ParseFloat(strings.ReplaceAll(n.Value, "_", ""), 64)
		if err != nil {
			return nil, errAt(n, "number %q is not representable in JSON", n.Value)
		}
		return json.Number(strconv.FormatFloat(f, 'g', -1, 64)), nil
	default:
		return nil, errAt(n, "unsupported YAML tag %s", n.ShortTag())
	}
}

func integer(n *yaml.Node) (any, error) {
	s := strings.ReplaceAll(n.Value, "_", "")
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimLeft(s, "+-")
	base := 10
	switch {
	case strings.HasPrefix(s, "0x"):
		base, s = 16, s[2:]
	case strings.HasPrefix(s, "0o"):
		base, s = 8, s[2:]
	case strings.HasPrefix(s, "0b"):
		base, s = 2, s[2:]
	}
	i, ok := new(big.Int).SetString(s, base)
	if !ok {
		return nil, errAt(n, "invalid integer %q", n.Value)
	}
	if neg {
		i.Neg(i)
	}
	return json.Number(i.String()), nil
}

func escape(key string) string {
	return strings.ReplaceAll(strings.ReplaceAll(key, "~", "~0"), "/", "~1")
}

func JSON(data []byte) (Doc, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return Doc{}, &Error{Msg: "invalid JSON: " + err.Error()}
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return Doc{}, &Error{Msg: "invalid JSON: trailing data after document"}
	}
	return Doc{Value: v}, nil
}

func JSONLines(data []byte) ([]Doc, error) {
	if len(data) == 0 {
		return nil, nil
	}
	if data[len(data)-1] != '\n' {
		return nil, &Error{Line: bytes.Count(data, []byte("\n")) + 1, Column: 1, Msg: "partial final line (no trailing newline)"}
	}
	lines := bytes.Split(data[:len(data)-1], []byte("\n"))
	docs := make([]Doc, 0, len(lines))
	for i, line := range lines {
		d, err := JSON(line)
		if err != nil {
			return nil, &Error{Line: i + 1, Column: 1, Msg: err.Error()}
		}
		if _, ok := d.Value.(map[string]any); !ok {
			return nil, &Error{Line: i + 1, Column: 1, Msg: "line is not a JSON object"}
		}
		docs = append(docs, d)
	}
	return docs, nil
}
