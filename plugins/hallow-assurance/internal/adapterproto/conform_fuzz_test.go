package adapterproto

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/schemas"
)

type conformKind struct {
	kind schemas.Kind
	sub  string
	val  func() any
}

var conformKinds = []conformKind{
	{schemas.AdapterDescribe, "describe", func() any { return &Describe{} }},
	{schemas.AdapterClassify, "classify", func() any {
		return &struct {
			Files []File `json:"files"`
		}{}
	}},
	{schemas.AdapterRun, "run", func() any { return &Run{} }},
	{schemas.AdapterTools, "tools", func() any { return &Tools{} }},
}

func FuzzConform(f *testing.F) {
	for i, ck := range conformKinds {
		for _, set := range []string{"valid", "invalid"} {
			dir := filepath.Join("..", "..", "schemas", "testdata", string(ck.kind), set)
			entries, err := os.ReadDir(dir)
			if err != nil {
				f.Fatal(err)
			}
			for _, e := range entries {
				data, err := os.ReadFile(filepath.Join(dir, e.Name()))
				if err != nil {
					f.Fatal(err)
				}
				f.Add(append([]byte{byte(i)}, data...))
			}
		}
	}
	f.Add(append([]byte{2}, `{"protocol":1,"evidence":[{"type":"junit","path":"junit.xml"}],"tool_versions":{"go":"go1.26.5"},"tool_versions":{"golangci-lint":"2.13.2"},"pinned_by":{"golangci-lint":"mise.toml"}}`...))
	const name = "assure-adapter-fuzz"
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) == 0 {
			return
		}
		ck := conformKinds[int(data[0])%len(conformKinds)]
		out := data[1:]
		err := conform(name, ck.sub, ck.kind, out, ck.val())
		if err != nil {
			e, ok := errors.AsType[*Error](err)
			if !ok || e.Adapter != name || e.Sub != ck.sub {
				t.Fatalf("%s error does not name %s %s: %#v", ck.kind, name, ck.sub, err)
			}
			return
		}
		vs, verr := schemas.Validate(ck.kind, out)
		if verr != nil || len(vs) > 0 {
			t.Fatalf("%s accepted %q, but the schema rejects it: err=%v violations=%v", ck.kind, out, verr, vs)
		}
	})
}
