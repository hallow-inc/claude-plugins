package decode

import (
	"encoding/json"
	"errors"
	"math"
	"math/big"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
	"pgregory.net/rapid"
)

var keyGen = rapid.StringMatching(`[a-z_][a-z0-9_]{0,8}`)

func valueGen(depth int) *rapid.Generator[any] {
	return rapid.Custom(func(t *rapid.T) any {
		kinds := 5
		if depth > 0 {
			kinds = 7
		}
		switch rapid.IntRange(0, kinds-1).Draw(t, "kind") {
		case 0:
			return nil
		case 1:
			return rapid.Bool().Draw(t, "bool")
		case 2:
			return rapid.Int64().Draw(t, "int")
		case 3:
			return rapid.Float64().Filter(func(f float64) bool {
				return !math.IsNaN(f) && !math.IsInf(f, 0) && !(f == 0 && math.Signbit(f))
			}).Draw(t, "float")
		case 4:
			return rapid.StringOf(rapid.Rune().Filter(yamlPrintable)).Draw(t, "string")
		case 5:
			return rapid.SliceOfN(valueGen(depth-1), 0, 4).Draw(t, "seq")
		default:
			return rapid.MapOfN(keyGen, valueGen(depth-1), 0, 4).Draw(t, "map")
		}
	})
}

func yamlPrintable(r rune) bool {
	switch {
	case r == '\t' || r == '\n' || r == '\r':
		return true
	case r >= 0x20 && r <= 0x7E:
		return true
	case r == 0x2028 || r == 0x2029:
		return false
	case r >= 0xA0 && r <= 0xD7FF, r >= 0xE000 && r <= 0xFFFD && r != 0xFEFF:
		return true
	default:
		return r >= 0x10000 && r <= 0x10FFFF
	}
}

func marshalBlock(t *rapid.T, v any) []byte {
	src, err := yaml.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var probe any
	if yaml.Unmarshal(src, &probe) != nil || !reflect.DeepEqual(normalize(probe), normalize(v)) {
		t.Skip("yaml.v3 does not round-trip its own output for this value")
	}
	return src
}

func normalize(v any) any {
	switch x := v.(type) {
	case int:
		return new(big.Rat).SetInt64(int64(x)).RatString()
	case uint64:
		return new(big.Rat).SetUint64(x).RatString()
	case int64:
		return new(big.Rat).SetInt64(x).RatString()
	case float64:
		r, _ := new(big.Rat).SetString(strconv.FormatFloat(x, 'g', -1, 64))
		return r.RatString()
	case json.Number:
		r, ok := new(big.Rat).SetString(string(x))
		if !ok {
			return "unparseable:" + string(x)
		}
		return r.RatString()
	case []any:
		out := make([]any, len(x))
		for i := range x {
			out[i] = normalize(x[i])
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = normalize(e)
		}
		return out
	default:
		return v
	}
}

func TestYAMLRoundTripFromJSONSyntax(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		want := rapid.MapOfN(keyGen, valueGen(3), 0, 5).Draw(t, "doc")
		src, err := json.Marshal(want)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		doc, err := YAML(src)
		if err != nil {
			t.Fatalf("decode %s: %v", src, err)
		}
		if !reflect.DeepEqual(normalize(doc.Value), normalize(want)) {
			t.Fatalf("round trip changed value\nsrc: %s\ngot:  %#v\nwant: %#v", src, doc.Value, normalize(want))
		}
	})
}

func TestYAMLRoundTripFromBlockSyntax(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		want := rapid.MapOfN(keyGen, valueGen(3), 0, 5).Draw(t, "doc")
		src := marshalBlock(t, want)
		doc, err := YAML(src)
		if err != nil {
			t.Fatalf("decode %q: %v", src, err)
		}
		if !reflect.DeepEqual(normalize(doc.Value), normalize(want)) {
			t.Fatalf("round trip changed value\nsrc:\n%s\ngot:  %#v\nwant: %#v", src, doc.Value, normalize(want))
		}
	})
}

func TestEveryValueHasASourcePosition(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		src := marshalBlock(t, rapid.MapOfN(keyGen, valueGen(3), 0, 5).Draw(t, "doc"))
		doc, err := YAML(src)
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		var walk func(v any, ptr string)
		walk = func(v any, ptr string) {
			if p, ok := doc.Pos[ptr]; !ok || p.Line < 1 {
				t.Fatalf("no position for %q in\n%s", ptr, src)
			}
			switch x := v.(type) {
			case []any:
				for i, e := range x {
					walk(e, ptr+"/"+strconv.Itoa(i))
				}
			case map[string]any:
				for k, e := range x {
					walk(e, ptr+"/"+escape(k))
				}
			}
		}
		walk(doc.Value, "")
	})
}

func TestUnquotedDateDecodesAsItsText(t *testing.T) {
	doc, err := YAML([]byte("expires: 2026-12-31\nat: 2026-12-31T10:00:00Z\n"))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"expires": "2026-12-31", "at": "2026-12-31T10:00:00Z"}
	if !reflect.DeepEqual(doc.Value, want) {
		t.Fatalf("got %#v", doc.Value)
	}
}

func TestLargeIntegerStaysExact(t *testing.T) {
	doc, err := YAML([]byte("n: 99999999999999999999\nh: 0x1F\n"))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"n": json.Number("99999999999999999999"), "h": json.Number("31")}
	if !reflect.DeepEqual(doc.Value, want) {
		t.Fatalf("got %#v", doc.Value)
	}
}

func TestRejections(t *testing.T) {
	cases := []struct {
		name, src, msg string
		line           int
	}{
		{"duplicate key", "default_level: B\nversion: 0\ndefault_level: C\n", "duplicate key \"default_level\" (first at line 1)", 3},
		{"alias", "a: &x {k: 1}\nb: *x\n", "anchors and aliases", 1},
		{"alias only", "a: 1\nb: *x\n", "", 0},
		{"merge key", "base: {k: 1}\nobj:\n  <<: {k: 2}\n", "merge keys", 3},
		{"two documents", "a: 1\n---\nb: 2\n", "more than one YAML document", 2},
		{"empty", "", "empty document", 0},
		{"infinity", "x: .inf\n", "not representable in JSON", 1},
		{"nan", "x: .nan\n", "not representable in JSON", 1},
		{"custom tag", "x: !foo bar\n", "unsupported YAML tag", 1},
		{"binary", "x: !!binary aGk=\n", "unsupported YAML tag", 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := YAML([]byte(c.src))
			var de *Error
			if !errors.As(err, &de) {
				t.Fatalf("want *Error, got %v", err)
			}
			if !strings.Contains(de.Msg, c.msg) {
				t.Fatalf("message %q lacks %q", de.Msg, c.msg)
			}
			if c.line != 0 && de.Line != c.line {
				t.Fatalf("line %d, want %d", de.Line, c.line)
			}
		})
	}
}

func TestJSONRejectsTrailingData(t *testing.T) {
	if _, err := JSON([]byte(`{"a":1} {"b":2}`)); err == nil {
		t.Fatal("accepted two JSON values")
	}
}

func TestJSONKeepsNumbersExact(t *testing.T) {
	doc, err := JSON([]byte(`{"n":99999999999999999999}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := doc.Value.(map[string]any)["n"]; got != json.Number("99999999999999999999") {
		t.Fatalf("got %#v", got)
	}
}

func TestJSONLinesErrorsCarryLineNumbers(t *testing.T) {
	cases := []struct {
		name, src string
		line      int
	}{
		{"truncated final line", "{\"v\":0}\n{\"v\":0,\"session\":\"s1\"", 2},
		{"blank line", "{\"v\":0}\n\n{\"v\":0}\n", 2},
		{"not an object", "{\"v\":0}\n[1]\n", 2},
		{"garbage", "nope\n", 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := JSONLines([]byte(c.src))
			var de *Error
			if !errors.As(err, &de) || de.Line != c.line {
				t.Fatalf("got %v, want error at line %d", err, c.line)
			}
		})
	}
}

type jsonObject struct {
	ptr string
	m   map[string]any
}

func collectObjects(v any, ptr string, out []jsonObject) []jsonObject {
	switch x := v.(type) {
	case []any:
		for i, e := range x {
			out = collectObjects(e, ptr+"/"+strconv.Itoa(i), out)
		}
	case map[string]any:
		out = append(out, jsonObject{ptr, x})
		for k, e := range x {
			out = collectObjects(e, ptr+"/"+escape(k), out)
		}
	}
	return out
}

func encodeWithDuplicate(t *rapid.T, b *strings.Builder, v any, ptr, dupPtr, dupKey string) {
	switch x := v.(type) {
	case []any:
		b.WriteByte('[')
		for i, e := range x {
			if i > 0 {
				b.WriteByte(',')
			}
			encodeWithDuplicate(t, b, e, ptr+"/"+strconv.Itoa(i), dupPtr, dupKey)
		}
		b.WriteByte(']')
	case map[string]any:
		b.WriteByte('{')
		if ptr == dupPtr {
			b.WriteString(strconv.Quote(dupKey) + ":0,")
		}
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		for i, k := range keys {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(strconv.Quote(k) + ":")
			encodeWithDuplicate(t, b, x[k], ptr+"/"+escape(k), dupPtr, dupKey)
		}
		b.WriteByte('}')
	default:
		src, err := json.Marshal(x)
		if err != nil {
			t.Fatalf("marshal %#v: %v", x, err)
		}
		b.Write(src)
	}
}

func TestJSONRejectsADuplicateKeyAtAnyDepth(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		root := rapid.MapOfN(keyGen, valueGen(3), 0, 5).Draw(t, "doc")
		clean, err := json.Marshal(root)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		doc, err := JSON(clean)
		if err != nil {
			t.Fatalf("rejected a document without duplicates %s: %v", clean, err)
		}
		if !reflect.DeepEqual(normalize(doc.Value), normalize(root)) {
			t.Fatalf("round trip changed value\nsrc: %s\ngot: %#v", clean, doc.Value)
		}

		objs := collectObjects(root, "", nil)
		target := objs[rapid.IntRange(0, len(objs)-1).Draw(t, "object")]
		key := keyGen.Draw(t, "dupKey")
		if _, ok := target.m[key]; !ok {
			target.m[key] = nil
		}
		var b strings.Builder
		encodeWithDuplicate(t, &b, root, "", target.ptr, key)
		wantPtr := target.ptr + "/" + escape(key)

		_, err = JSON([]byte(b.String()))
		if err == nil {
			t.Fatalf("accepted duplicate key %q at %s in %s; the last value would silently replace the first", key, wantPtr, b.String())
		}
		if !strings.Contains(err.Error(), key) || !strings.Contains(err.Error(), wantPtr) {
			t.Fatalf("error %q does not name key %q and pointer %s", err, key, wantPtr)
		}
	})
}

func TestJSONLinesRejectsARepeatedKeyOnItsLine(t *testing.T) {
	_, err := JSONLines([]byte("{\"v\":0}\n{\"v\":0,\"session\":\"s1\"}\n{\"v\":0,\"session\":\"s1\",\"session\":\"s2\"}\n"))
	var de *Error
	if !errors.As(err, &de) {
		t.Fatalf("got %v, want *Error for the repeated key on line 3", err)
	}
	if de.Line != 3 || !strings.Contains(de.Error(), "line 3") || !strings.Contains(de.Msg, "session") {
		t.Fatalf("error %q (line %d) does not name line 3 and key session", de, de.Line)
	}
}

func TestJSONLinesAcceptsObjects(t *testing.T) {
	docs, err := JSONLines([]byte("{\"a\":1}\n{\"b\":2}\n"))
	if err != nil || len(docs) != 2 {
		t.Fatalf("got %d docs, err %v", len(docs), err)
	}
}
