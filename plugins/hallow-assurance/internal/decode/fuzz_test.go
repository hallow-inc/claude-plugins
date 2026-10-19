package decode

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

var sharedSeeds = []string{
	"",
	"---\n",
	`{"_":0,"_":null}`,
	"{\"v\":0}\n{\"v\":0,\"session\":\"s1\"}\n{\"v\":0,\"session\":\"s1\",\"session\":\"s2\"}\n",
}

func addFixtureSeeds(f *testing.F, exts ...string) {
	f.Helper()
	n := 0
	for _, ext := range exts {
		for _, dir := range []string{"valid", "invalid"} {
			paths, err := filepath.Glob(filepath.Join("..", "..", "schemas", "testdata", "*", dir, "*"+ext))
			if err != nil {
				f.Fatal(err)
			}
			for _, p := range paths {
				b, err := os.ReadFile(p)
				if err != nil {
					f.Fatal(err)
				}
				f.Add(b)
				n++
			}
		}
	}
	if n == 0 {
		f.Fatalf("no %v fixtures under schemas/testdata; the seed corpus would be empty", exts)
	}
	for _, s := range sharedSeeds {
		f.Add([]byte(s))
	}
}

func requireDecodeError(t *testing.T, err error) {
	t.Helper()
	var de *Error
	if !errors.As(err, &de) {
		t.Fatalf("error %v (%T) is not a *decode.Error", err, err)
	}
}

func FuzzYAML(f *testing.F) {
	addFixtureSeeds(f, ".yaml")
	f.Fuzz(func(t *testing.T, data []byte) {
		doc, err := YAML(data)
		if err != nil {
			requireDecodeError(t, err)
			return
		}
		if _, ok := doc.Pos[""]; !ok {
			t.Fatalf("decoded %q without a position for the root", data)
		}
	})
}

func referenceJSON(data []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("trailing data")
	}
	return v, nil
}

func objectKeyTokens(data []byte) (int, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var stack []bool
	keys := 0
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return keys, nil
		}
		if err != nil {
			return 0, err
		}
		switch tok {
		case json.Delim('{'):
			stack = append(stack, true)
			continue
		case json.Delim('['):
			stack = append(stack, false)
			continue
		case json.Delim('}'), json.Delim(']'):
			stack = stack[:len(stack)-1]
			continue
		}
		if _, ok := tok.(string); ok && len(stack) > 0 && stack[len(stack)-1] {
			keys++
			var member json.RawMessage
			if dec.More() {
				if err := dec.Decode(&member); err != nil {
					return 0, err
				}
				n, err := objectKeyTokens(member)
				if err != nil {
					return 0, err
				}
				keys += n
			}
		}
	}
}

func mapEntries(v any) int {
	switch x := v.(type) {
	case []any:
		n := 0
		for _, e := range x {
			n += mapEntries(e)
		}
		return n
	case map[string]any:
		n := len(x)
		for _, e := range x {
			n += mapEntries(e)
		}
		return n
	default:
		return 0
	}
}

func FuzzJSON(f *testing.F) {
	addFixtureSeeds(f, ".json")
	f.Fuzz(func(t *testing.T, data []byte) {
		doc, err := JSON(data)
		if err != nil {
			requireDecodeError(t, err)
			return
		}
		want, err := referenceJSON(data)
		if err != nil {
			t.Fatalf("decode.JSON accepted %q, encoding/json rejects it: %v", data, err)
		}
		if !reflect.DeepEqual(normalize(doc.Value), normalize(want)) {
			t.Fatalf("decode.JSON and encoding/json disagree on %q\ngot:  %#v\nwant: %#v", data, doc.Value, want)
		}
		keys, err := objectKeyTokens(data)
		if err != nil {
			t.Fatalf("key count over accepted input %q: %v", data, err)
		}
		if got := mapEntries(doc.Value); got != keys {
			t.Fatalf("accepted %q with %d object keys but %d decoded entries; a duplicate key replaced an earlier value", data, keys, got)
		}
	})
}

func FuzzReviewsAPI(f *testing.F) {
	pull, err := os.ReadFile(filepath.Join("testdata", "reviews-api", "pull.json"))
	if err != nil {
		f.Fatal(err)
	}
	pages, err := os.ReadFile(filepath.Join("testdata", "reviews-api", "pages.json"))
	if err != nil {
		f.Fatal(err)
	}
	f.Add(pull, pages)
	f.Add(pull, []byte(`[]`))
	f.Add(pull, []byte(`[[]]`))
	f.Add(pull, []byte(`[{"user":{"login":"a"},"state":"APPROVED","commit_id":"x"}]`))
	f.Add([]byte(`{"user":null,"head":{"sha":"x"}}`), pages)
	f.Add([]byte(`[]`), []byte(`[]`))
	f.Add([]byte(``), []byte(``))
	f.Fuzz(func(t *testing.T, pull, pages []byte) {
		doc, err := ReviewsAPI(pull, pages)
		if err != nil {
			return
		}
		if doc.Reviews == nil {
			t.Fatalf("accepted %q / %q with a nil reviews slice; it marshals as null and the reviews schema rejects it", pull, pages)
		}
		out, err := json.Marshal(doc)
		if err != nil {
			t.Fatalf("marshal %+v: %v", doc, err)
		}
		var back ReviewsDoc
		if err := json.Unmarshal(out, &back); err != nil {
			t.Fatalf("unmarshal own output %s: %v", out, err)
		}
		again, err := json.Marshal(back)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(out, again) || !reflect.DeepEqual(doc, back) {
			t.Fatalf("doc does not round-trip through encoding/json:\n%s\n%s", out, again)
		}
	})
}

func FuzzJSONLines(f *testing.F) {
	addFixtureSeeds(f, ".jsonl")
	f.Fuzz(func(t *testing.T, data []byte) {
		docs, err := JSONLines(data)
		if err != nil {
			requireDecodeError(t, err)
			return
		}
		if want := bytes.Count(data, []byte("\n")); len(docs) != want {
			t.Fatalf("decoded %d docs from %d newline-terminated lines in %q", len(docs), want, data)
		}
		for i, d := range docs {
			if _, ok := d.Value.(map[string]any); !ok {
				t.Fatalf("line %d of %q decoded to %T, not a JSON object", i+1, data, d.Value)
			}
		}
	})
}
