package schemas

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

type op struct {
	at  string
	key string
	del bool
	val any
}

type corruption struct {
	desc string
	ops  []op
	want string
}

type sample struct {
	doc any
	cs  []corruption
}

type rec struct{ cs []corruption }

func (r *rec) add(desc, want string, ops ...op) {
	r.cs = append(r.cs, corruption{desc: desc, ops: ops, want: want})
}

func (r *rec) root(vals ...any) {
	for _, v := range vals {
		r.add(fmt.Sprintf("root = %#v", v), "", op{val: v})
	}
}

func (r *rec) closed(ptr string) {
	r.add("unknown key at "+ptr, ptr, op{at: ptr, key: "zz_unknown", val: "x"})
}

func (r *rec) required(ptr string, keys ...string) {
	for _, k := range keys {
		r.add("drop "+ptr+"/"+k, ptr, op{at: ptr, key: k, del: true})
	}
}

func (r *rec) bad(ptr, key string, vals ...any) {
	for _, v := range vals {
		r.add(fmt.Sprintf("%s/%s = %#v", ptr, key, v), ptr+"/"+key, op{at: ptr, key: key, val: v})
	}
}

var (
	badLevel = []any{"E", "a", 1}
	badPath  = []any{"/abs", "a/../b", "..", `a\b`, "", 7}
	badObjID = []any{"ver-x", "VER", 7}
	badLang  = []any{"Go", "1go", 7}
	badEv    = []any{"Mutation", "a..b", 7}
	badText  = []any{"", 7}
	badBlob  = []any{"A41E", strings.Repeat("a", 39), 7}

	levelGen  = rapid.SampledFrom([]string{"A", "B", "C", "D"})
	pathGen   = rapid.StringMatching(`[a-z]{1,6}(/[a-z*]{1,6}){0,3}`)
	objIDGen  = rapid.StringMatching(`[A-Z]{2,5}(-[A-Z0-9]{1,6}){1,3}`)
	langGen   = rapid.StringMatching(`[a-z][a-z0-9_-]{0,8}`)
	evGen     = rapid.StringMatching(`[a-z][a-z0-9_]{0,6}(\.[a-z0-9_]{1,6}){0,2}`)
	textGen   = rapid.StringMatching(`[A-Za-z][A-Za-z0-9 ]{0,20}`)
	blobGen   = rapid.OneOf(rapid.StringMatching(`[0-9a-f]{40}`), rapid.StringMatching(`[0-9a-f]{64}`))
	levelKeys = []string{"A", "B", "C", "D"}
)

func levelSubset(t *rapid.T, label string, min int) []string {
	var out []string
	for _, l := range levelKeys {
		if rapid.Bool().Draw(t, label+l) {
			out = append(out, l)
		}
	}
	if len(out) < min {
		out = append(out, levelGen.Draw(t, label+"min"))
	}
	return out
}

func idx(i int) string { return strconv.Itoa(i) }

var manifestGen = rapid.Custom(func(t *rapid.T) sample {
	r := &rec{}
	doc := map[string]any{
		"version":       0,
		"catalog":       "v" + strconv.Itoa(rapid.IntRange(0, 9).Draw(t, "cat")),
		"default_level": levelGen.Draw(t, "dl"),
	}
	langs := rapid.SliceOfNDistinct(langGen, 1, 3, func(s string) string { return s }).Draw(t, "langs")
	la := make([]any, len(langs))
	for i, l := range langs {
		la[i] = l
		r.bad("/languages", idx(i), badLang...)
	}
	doc["languages"] = la
	r.root("x", 5, []any{})
	r.closed("")
	r.required("", "version", "catalog", "default_level", "languages", "components")
	r.bad("", "version", 1, "0")
	r.bad("", "catalog", "0", "vx", 0)
	r.bad("", "default_level", badLevel...)
	r.bad("", "languages", "x", []any{}, []any{langs[0], langs[0]})
	r.bad("", "components", "x", []any{"x"})
	r.bad("", "protected", "x")
	r.bad("", "human_review", "x", map[string]any{"E": "required"}, map[string]any{"a": "optional"})
	if rapid.Bool().Draw(t, "review") {
		hr := map[string]any{}
		for _, l := range levelKeys {
			if rapid.Bool().Draw(t, "hr"+l) {
				hr[l] = rapid.SampledFrom([]string{"required", "optional"}).Draw(t, "hrv")
				r.bad("/human_review", l, "off", true, 1)
			}
		}
		doc["human_review"] = hr
	}
	r.bad("", "provenance", "yes", 1)
	if rapid.Bool().Draw(t, "has provenance") {
		doc["provenance"] = rapid.Bool().Draw(t, "provenance")
	}
	if rapid.Bool().Draw(t, "protected") {
		prot := []any{}
		for i := range rapid.IntRange(0, 2).Draw(t, "nprot") {
			prot = append(prot, pathGen.Draw(t, "prot"))
			r.bad("/protected", idx(i), badPath...)
		}
		doc["protected"] = prot
	}
	comps := []any{}
	for i := range rapid.IntRange(0, 3).Draw(t, "ncomp") {
		p := "/components/" + idx(i)
		c := map[string]any{"path": pathGen.Draw(t, "path"), "level": levelGen.Draw(t, "lvl")}
		r.closed(p)
		r.required(p, "path", "level")
		r.bad(p, "path", badPath...)
		r.bad(p, "level", badLevel...)
		r.bad("/components", idx(i), "x")
		r.bad(p, "formal", "x", map[string]any{"model": "m", "challenge": "c", "link": "lean"})
		r.bad(p, "dst", "x", map[string]any{"harness": "/abs"})
		if rapid.Bool().Draw(t, "formal") {
			c["formal"] = map[string]any{"model": pathGen.Draw(t, "m"), "challenge": pathGen.Draw(t, "ch"), "link": rapid.SampledFrom([]string{"drt", "conformance", "none"}).Draw(t, "link")}
			fp := p + "/formal"
			r.closed(fp)
			r.required(fp, "model", "challenge", "link")
			r.bad(fp, "model", badPath...)
			r.bad(fp, "challenge", badPath...)
			r.bad(fp, "link", "lean", 1)
		}
		if rapid.Bool().Draw(t, "dst") {
			c["dst"] = map[string]any{"harness": pathGen.Draw(t, "h")}
			dp := p + "/dst"
			r.closed(dp)
			r.required(dp, "harness")
			r.bad(dp, "harness", badPath...)
		}
		comps = append(comps, c)
	}
	doc["components"] = comps
	return sample{doc, r.cs}
})

var catalogGen = rapid.Custom(func(t *rapid.T) sample {
	r := &rec{}
	r.root("x", map[string]any{}, []any{})
	var objs []any
	for i := range rapid.IntRange(1, 3).Draw(t, "nobj") {
		p := "/" + idx(i)
		o := map[string]any{
			"id":       objIDGen.Draw(t, "id"),
			"title":    textGen.Draw(t, "title"),
			"source":   []any{textGen.Draw(t, "src")},
			"evidence": evGen.Draw(t, "ev"),
		}
		levels := map[string]any{}
		for _, l := range levelSubset(t, "lv", 1) {
			levels[l] = rapid.SampledFrom([]string{"required", "advisory"}).Draw(t, "st")
			r.bad(p+"/levels", l, "mandatory", 1)
		}
		o["levels"] = levels
		r.bad("", idx(i), "x")
		r.closed(p)
		r.required(p, "id", "title", "source", "levels", "evidence")
		r.bad(p, "id", badObjID...)
		r.bad(p, "title", badText...)
		r.bad(p, "source", "x", []any{}, []any{""}, []any{7})
		r.bad(p, "levels", "x", map[string]any{}, map[string]any{"E": "required"}, map[string]any{"A": "mandatory"})
		r.bad(p, "evidence", badEv...)
		r.bad(p, "independence", "x", map[string]any{"E": "tier1"}, map[string]any{"A": "tier4"})
		r.bad(p, "threshold", "x", map[string]any{"E": 1}, map[string]any{"A": 101}, map[string]any{"A": -1}, map[string]any{"A": "80"})
		r.bad(p, "implementations", "x", map[string]any{"Go": "t"}, map[string]any{"go": ""}, map[string]any{"go": 7})
		r.bad(p, "alternative_for", badObjID...)
		r.bad(p, "applies_to", "lean", 1)
		if rapid.Bool().Draw(t, "ind") {
			ind := map[string]any{}
			for _, l := range levelSubset(t, "ind", 1) {
				ind[l] = rapid.SampledFrom([]string{"tier1", "tier2", "tier3"}).Draw(t, "tier")
			}
			o["independence"] = ind
		}
		if rapid.Bool().Draw(t, "thr") {
			thr := map[string]any{}
			for _, l := range levelSubset(t, "thr", 1) {
				thr[l] = rapid.IntRange(0, 100).Draw(t, "pct")
			}
			o["threshold"] = thr
		}
		if rapid.Bool().Draw(t, "impl") {
			o["implementations"] = map[string]any{langGen.Draw(t, "lang"): textGen.Draw(t, "tool")}
		}
		if rapid.Bool().Draw(t, "alt") {
			o["alternative_for"] = objIDGen.Draw(t, "alt")
		}
		if rapid.Bool().Draw(t, "applies") {
			o["applies_to"] = rapid.SampledFrom([]string{"formal", "dst"}).Draw(t, "applies")
		}
		objs = append(objs, o)
	}
	return sample{objs, r.cs}
})

var waiversGen = rapid.Custom(func(t *rapid.T) sample {
	r := &rec{}
	r.root("x", map[string]any{})
	var ws []any
	for i := range rapid.IntRange(1, 3).Draw(t, "nw") {
		p := "/" + idx(i)
		ws = append(ws, map[string]any{
			"objective": objIDGen.Draw(t, "obj"),
			"scope":     pathGen.Draw(t, "scope"),
			"rationale": rapid.StringMatching(`[A-Za-z][A-Za-z ]{19,50}`).Draw(t, "why"),
			"approver":  rapid.StringMatching(`@?[a-z][a-z0-9]{2,10}`).Draw(t, "who"),
			"expires": fmt.Sprintf("%04d-%02d-%02d",
				rapid.IntRange(2000, 2099).Draw(t, "y"), rapid.IntRange(1, 12).Draw(t, "m"), rapid.IntRange(1, 28).Draw(t, "d")),
		})
		r.bad("", idx(i), "x")
		r.closed(p)
		r.required(p, "objective", "scope", "rationale", "approver", "expires")
		r.bad(p, "objective", badObjID...)
		r.bad(p, "scope", badPath...)
		r.bad(p, "rationale", "short", 7)
		r.bad(p, "approver", "TODO", "has space", "-lead", 7)
		r.bad(p, "expires", "2026-02-30", "2026-1-1", 20261231)
	}
	return sample{ws, r.cs}
})

var provenanceGen = rapid.Custom(func(t *rapid.T) sample {
	r := &rec{}
	doc := map[string]any{
		"v":       0,
		"session": textGen.Draw(t, "session"),
		"tool":    rapid.SampledFrom([]string{"Edit", "Write", "NotebookEdit"}).Draw(t, "tool"),
		"path":    pathGen.Draw(t, "path"),
		"role":    rapid.SampledFrom([]string{"source", "test", "generated", "fuzz_corpus", "config", "unclassified"}).Draw(t, "role"),
	}
	switch rapid.IntRange(0, 2).Draw(t, "shape") {
	case 0:
		doc["pre"], doc["post"] = nil, blobGen.Draw(t, "post")
	case 1:
		doc["pre"], doc["post"] = blobGen.Draw(t, "pre"), nil
	default:
		doc["pre"], doc["post"] = blobGen.Draw(t, "pre"), blobGen.Draw(t, "post")
	}
	if rapid.Bool().Draw(t, "sub") {
		doc["agent_id"] = textGen.Draw(t, "aid")
		doc["agent_type"] = "hallow-assurance:" + textGen.Draw(t, "atype")
	}
	r.closed("")
	r.required("", "v", "session", "tool", "path", "role", "pre", "post")
	r.bad("", "v", 1, "0")
	r.bad("", "session", badText...)
	r.bad("", "tool", badText...)
	r.bad("", "path", badPath...)
	r.bad("", "role", "src", 1)
	r.bad("", "pre", badBlob...)
	r.bad("", "post", badBlob...)
	r.bad("", "agent_id", badText...)
	r.bad("", "agent_type", badText...)
	r.add("pre and post both null", "", op{key: "pre", val: nil}, op{key: "post", val: nil})
	return sample{doc, r.cs}
})

var describeGen = rapid.Custom(func(t *rapid.T) sample {
	r := &rec{}
	langs := rapid.SliceOfNDistinct(langGen, 1, 3, func(s string) string { return s }).Draw(t, "langs")
	la := make([]any, len(langs))
	for i, l := range langs {
		la[i] = l
		r.bad("/languages", idx(i), badLang...)
	}
	patterns := map[string]any{}
	for _, k := range []string{"test", "generated", "fuzz_corpus", "config"} {
		if rapid.Bool().Draw(t, k) {
			patterns[k] = []any{pathGen.Draw(t, k+"glob")}
		}
	}
	objectives := map[string]any{}
	for range rapid.IntRange(0, 2).Draw(t, "nobj") {
		entry := map[string]any{"tool": textGen.Draw(t, "tool")}
		if rapid.Bool().Draw(t, "hasfast") {
			entry["fast"] = rapid.Bool().Draw(t, "fast")
		}
		objectives[objIDGen.Draw(t, "oid")] = entry
	}
	claims := []any{}
	for i := range rapid.IntRange(1, 2).Draw(t, "nclaims") {
		claims = append(claims, pathGen.Draw(t, "claim"))
		r.bad("/claims", idx(i), badPath...)
	}
	doc := map[string]any{"protocol": 1, "languages": la, "claims": claims, "patterns": patterns, "objectives": objectives}
	if rapid.Bool().Draw(t, "hasref") {
		doc["reference"] = rapid.SampledFrom(langs).Draw(t, "ref")
	}
	r.bad("", "reference", badLang...)
	r.root("x", []any{})
	r.closed("")
	r.required("", "protocol", "languages", "claims", "patterns", "objectives")
	r.bad("", "claims", "x", []any{})
	r.bad("", "protocol", badProtocolV1...)
	r.bad("", "languages", "x", []any{}, []any{langs[0], langs[0]})
	r.bad("", "patterns", "x", map[string]any{"source": []any{}}, map[string]any{"test": "x"}, map[string]any{"test": []any{"/abs"}})
	r.bad("", "objectives", "x", map[string]any{"ver-x": map[string]any{"tool": "t"}}, map[string]any{"VER-X": "x"},
		map[string]any{"VER-X": map[string]any{}}, map[string]any{"VER-X": map[string]any{"tool": ""}},
		map[string]any{"VER-X": map[string]any{"tool": "t", "zz": 1}},
		map[string]any{"VER-X": map[string]any{"tool": "t", "fast": "yes"}})
	return sample{doc, r.cs}
})

var classifyGen = rapid.Custom(func(t *rapid.T) sample {
	r := &rec{}
	var files []any
	for i := range rapid.IntRange(0, 3).Draw(t, "nf") {
		p := "/files/" + idx(i)
		files = append(files, map[string]any{
			"path":     pathGen.Draw(t, "path"),
			"language": langGen.Draw(t, "lang"),
			"role":     rapid.SampledFrom([]string{"source", "test", "generated", "fuzz_corpus", "config"}).Draw(t, "role"),
		})
		r.closed(p)
		r.required(p, "path", "language", "role")
		r.bad(p, "path", badPath...)
		r.bad(p, "language", badLang...)
		r.bad(p, "role", "src", 1)
	}
	if files == nil {
		files = []any{}
	}
	doc := map[string]any{"protocol": 0, "files": files}
	r.root("x", []any{})
	r.closed("")
	r.required("", "protocol", "files")
	r.bad("", "protocol", 1, -1, "0")
	r.bad("", "files", "x", []any{"x"})
	return sample{doc, r.cs}
})

var runGen = rapid.Custom(func(t *rapid.T) sample {
	r := &rec{}
	tv := map[string]any{}
	for range rapid.IntRange(1, 3).Draw(t, "ntv") {
		tv[langGen.Draw(t, "tool")] = textGen.Draw(t, "ver")
	}
	doc := map[string]any{"protocol": 1, "evidence": []any{}, "tool_versions": tv, "pinned_by": pinsOf(t, tv)}
	if env := environmentOf(t, r); env != nil {
		doc["environment"] = env
		r.bad("", "evidence", []any{map[string]any{"type": "test.junit", "path": "junit.xml"}})
		if rapid.Bool().Draw(t, "notools") {
			doc["tool_versions"], doc["pinned_by"] = map[string]any{}, map[string]any{}
		}
	} else {
		ev := []any{}
		for i := range rapid.IntRange(0, 3).Draw(t, "ne") {
			p := "/evidence/" + idx(i)
			ev = append(ev, map[string]any{"type": evGen.Draw(t, "type"), "path": pathGen.Draw(t, "path")})
			r.closed(p)
			r.required(p, "type", "path")
			r.bad(p, "type", badEv...)
			r.bad(p, "path", badPath...)
		}
		doc["evidence"] = ev
		r.bad("", "tool_versions", map[string]any{})
	}
	r.root("x", []any{})
	r.closed("")
	r.required("", "protocol", "evidence", "tool_versions", "pinned_by")
	r.bad("", "protocol", badProtocolV1...)
	r.bad("", "evidence", "x", []any{"x"})
	r.bad("", "tool_versions", "x", map[string]any{"go": ""}, map[string]any{"go": 7})
	r.bad("", "pinned_by", "x", map[string]any{"go": ""}, map[string]any{"go": 7})
	return sample{doc, r.cs}
})

var badProtocolV1 = []any{0, 2, "1"}

func pinsOf(t *rapid.T, tv map[string]any) map[string]any {
	pins := map[string]any{}
	for _, tool := range slices.Sorted(maps.Keys(tv)) {
		if rapid.Bool().Draw(t, "pinned "+tool) {
			pins[tool] = rapid.SampledFrom([]string{"mise.toml", ".tool-versions", "go.mod", "default"}).Draw(t, "pin")
		}
	}
	return pins
}

func environmentOf(t *rapid.T, r *rec) map[string]any {
	r.bad("", "environment", "x", map[string]any{}, map[string]any{"message": ""}, map[string]any{"message": 7}, map[string]any{"message": "m", "zz": 1})
	if !rapid.Bool().Draw(t, "environment") {
		return nil
	}
	r.closed("/environment")
	r.required("/environment", "message")
	return map[string]any{"message": textGen.Draw(t, "environment message")}
}

var toolsGen = rapid.Custom(func(t *rapid.T) sample {
	r := &rec{}
	tools := []any{}
	entry := func() map[string]any {
		return map[string]any{
			"name":      langGen.Draw(t, "name"),
			"version":   rapid.StringMatching(`[0-9]{1,2}\.[0-9]{1,2}\.[0-9]{1,2}`).Draw(t, "version"),
			"pinned_by": rapid.SampledFrom([]string{"mise.toml", ".tool-versions", "default"}).Draw(t, "pinned_by"),
			"install":   rapid.StringMatching(`[a-z][a-z0-9 @./:-]{0,40}`).Draw(t, "install"),
		}
	}
	doc := map[string]any{"protocol": 1}
	if env := environmentOf(t, r); env != nil {
		doc["environment"] = env
		r.bad("", "tools", []any{entry()})
	} else {
		for i := range rapid.IntRange(0, 3).Draw(t, "ntools") {
			p := "/tools/" + idx(i)
			tools = append(tools, entry())
			r.closed(p)
			r.required(p, "name", "version", "pinned_by", "install")
			r.bad(p, "name", badText...)
			r.bad(p, "version", badText...)
			r.bad(p, "pinned_by", badText...)
			r.bad(p, "install", "", "go install x\nrm -rf /", "a\rb", 7)
			r.bad("/tools", idx(i), "x")
		}
	}
	doc["tools"] = tools
	r.root("x", []any{})
	r.closed("")
	r.required("", "protocol", "tools")
	r.bad("", "protocol", badProtocolV1...)
	r.bad("", "tools", "x", map[string]any{})
	return sample{doc, r.cs}
})

var cacheGen = rapid.Custom(func(t *rapid.T) sample {
	r := &rec{}
	adapters := map[string]any{}
	for _, lang := range rapid.SliceOfNDistinct(langGen, 0, 2, func(s string) string { return s }).Draw(t, "langs") {
		p := "/adapters/" + lang
		d := describeGen.Draw(t, "describe")
		adapters[lang] = map[string]any{"path": "/bin/assure-adapter-" + lang, "sha256": rapid.StringMatching(`[0-9a-f]{64}`).Draw(t, "sha"), "describe": d.doc}
		r.closed(p)
		r.required(p, "path", "sha256", "describe")
		r.bad("/adapters", lang, "x")
		r.bad(p, "path", badText...)
		r.bad(p, "sha256", strings.Repeat("A", 64), strings.Repeat("a", 63), 7)
		for _, c := range d.cs {
			ops := make([]op, len(c.ops))
			for i, o := range c.ops {
				if o.at == "" && o.key == "" {
					ops[i] = op{at: p, key: "describe", val: o.val}
				} else {
					ops[i] = op{at: p + "/describe" + o.at, key: o.key, del: o.del, val: o.val}
				}
			}
			r.add("describe: "+c.desc, p+"/describe"+c.want, ops...)
		}
	}
	doc := map[string]any{"version": 0, "adapters": adapters}
	r.root("x", []any{})
	r.closed("")
	r.required("", "version", "adapters")
	r.bad("", "version", 1, "0")
	r.bad("", "adapters", "x", map[string]any{"Go": map[string]any{}})
	return sample{doc, r.cs}
})

var snapshotGen = rapid.Custom(func(t *rapid.T) sample {
	r := &rec{}
	files := []any{}
	for i := range rapid.IntRange(0, 3).Draw(t, "nfiles") {
		p := "/files/" + idx(i)
		files = append(files, map[string]any{"path": pathGen.Draw(t, "path"), "blob": rapid.StringMatching(`[0-9a-f]{40}`).Draw(t, "blob")})
		r.closed(p)
		r.required(p, "path", "blob")
		r.bad(p, "path", badPath...)
		r.bad(p, "blob", strings.Repeat("A", 40), strings.Repeat("a", 39), 7)
		r.bad("/files", idx(i), "x")
	}
	doc := map[string]any{"version": 0, "session": rapid.StringMatching(`[A-Za-z0-9_-]{1,40}`).Draw(t, "session"), "files": files}
	r.root("x", []any{})
	r.closed("")
	r.required("", "version", "session", "files")
	r.bad("", "version", 1, "0")
	r.bad("", "session", "", "a/b", "..", strings.Repeat("a", 129), 7)
	r.bad("", "files", "x", map[string]any{})
	return sample{doc, r.cs}
})

var stopStateGen = rapid.Custom(func(t *rapid.T) sample {
	r := &rec{}
	fps := rapid.SliceOfNDistinct(rapid.StringMatching(`[0-9a-f]{64}`), 1, 32, rapid.ID[string]).Draw(t, "fingerprints")
	entries := make([]any, len(fps))
	for i, f := range fps {
		entries[i] = map[string]any{"fingerprint": f, "blocks": rapid.IntRange(1, 3).Draw(t, "fp blocks")}
	}
	tooMany := make([]any, 33)
	for i := range tooMany {
		tooMany[i] = map[string]any{"fingerprint": fmt.Sprintf("%064x", i), "blocks": 1}
	}
	doc := map[string]any{"version": 1, "blocks": rapid.IntRange(0, 10).Draw(t, "blocks"), "fingerprints": entries}
	r.root("x", []any{})
	r.closed("")
	r.required("", "version", "blocks")
	r.bad("", "version", 0, 2, "1")
	r.bad("", "blocks", -1, 1.5, "1")
	r.bad("", "fingerprints", "x", tooMany)
	r.bad("/fingerprints", "0", "x")
	r.closed("/fingerprints/0")
	r.required("/fingerprints/0", "fingerprint", "blocks")
	r.bad("/fingerprints/0", "fingerprint", 7, fps[0][:63], fps[0][:63]+"G")
	r.bad("/fingerprints/0", "blocks", 0, 4, 1.5)
	return sample{doc, r.cs}
})

var shaGen = rapid.OneOf(rapid.StringMatching(`[0-9a-f]{40}`), rapid.StringMatching(`[0-9a-f]{64}`))

var badSHA = []any{"7330276", strings.Repeat("A", 40), strings.Repeat("a", 41), 7}

func baselineEntry(t *rapid.T, r *rec, p string) map[string]any {
	r.closed(p)
	r.required(p, "objective", "rule", "path", "message", "count")
	r.bad(p, "objective", badObjID...)
	r.bad(p, "rule", badText...)
	r.bad(p, "path", badPath...)
	r.bad(p, "message", 7)
	r.bad(p, "count", 0, 1.5, "1")
	return map[string]any{
		"objective": objIDGen.Draw(t, "obj"),
		"rule":      textGen.Draw(t, "rule"),
		"path":      pathGen.Draw(t, "path"),
		"message":   rapid.StringMatching(`[A-Za-z ]{0,20}`).Draw(t, "msg"),
		"count":     rapid.IntRange(1, 5).Draw(t, "count"),
	}
}

func waiverEntry(t *rapid.T, r *rec, p string) map[string]any {
	r.closed(p)
	r.bad(p, "expires", "2026-02-30")
	return map[string]any{
		"objective": objIDGen.Draw(t, "wobj"),
		"scope":     pathGen.Draw(t, "wscope"),
		"rationale": rapid.StringMatching(`[A-Za-z][A-Za-z ]{19,30}`).Draw(t, "why"),
		"approver":  rapid.StringMatching(`@?[a-z][a-z0-9]{2,10}`).Draw(t, "who"),
		"expires":   "2026-12-31",
	}
}

var baselineGen = rapid.Custom(func(t *rapid.T) sample {
	r := &rec{}
	entries := []any{}
	for i := range rapid.IntRange(0, 3).Draw(t, "n") {
		entries = append(entries, baselineEntry(t, r, "/entries/"+idx(i)))
		r.bad("/entries", idx(i), "x")
	}
	doc := map[string]any{"version": 0, "entries": entries}
	r.root("x", []any{})
	r.closed("")
	r.required("", "version", "entries")
	r.bad("", "version", 1, "0")
	r.bad("", "entries", "x", map[string]any{})
	return sample{doc, r.cs}
})

var reportGen = rapid.Custom(func(t *rapid.T) sample {
	r := &rec{}
	tv := map[string]any{}
	pins := map[string]any{}
	for _, lang := range rapid.SliceOfNDistinct(langGen, 0, 2, func(s string) string { return s }).Draw(t, "langs") {
		tv[lang] = map[string]any{textGen.Draw(t, "tool"): textGen.Draw(t, "ver")}
		pins[lang] = pinsOf(t, tv[lang].(map[string]any))
		r.bad("/tool_versions", lang, "x", map[string]any{"go": ""}, map[string]any{"go": 7})
		r.bad("/pinned_by", lang, "x", map[string]any{"go": ""}, map[string]any{"go": 7})
	}
	probs := []any{}
	for i := range rapid.IntRange(0, 2).Draw(t, "np") {
		probs = append(probs, textGen.Draw(t, "problem"))
		r.bad("/problems", idx(i), badText...)
	}
	expired := []any{}
	for i := range rapid.IntRange(0, 2).Draw(t, "ne") {
		expired = append(expired, waiverEntry(t, r, "/expired_waivers/"+idx(i)))
	}
	removable := []any{}
	for i := range rapid.IntRange(0, 2).Draw(t, "nr") {
		p := "/removable_baseline/" + idx(i)
		removable = append(removable, map[string]any{"entry": baselineEntry(t, r, p+"/entry"), "unused": rapid.IntRange(1, 3).Draw(t, "unused")})
		r.closed(p)
		r.required(p, "entry", "unused")
		r.bad(p, "unused", 0, 1.5, "1")
	}
	objs := []any{}
	for i := range rapid.IntRange(0, 3).Draw(t, "no") {
		p := "/objectives/" + idx(i)
		ws := []any{}
		for j := range rapid.IntRange(0, 1).Draw(t, "nw") {
			ws = append(ws, waiverEntry(t, r, p+"/waivers/"+idx(j)))
		}
		o := map[string]any{
			"objective":       objIDGen.Draw(t, "oid"),
			"status":          rapid.SampledFrom([]string{"pass", "fail", "advisory-fail", "waived"}).Draw(t, "status"),
			"details":         []any{textGen.Draw(t, "detail")},
			"waivers":         ws,
			"baselined":       rapid.IntRange(0, 5).Draw(t, "bl"),
			"under_threshold": rapid.IntRange(0, 5).Draw(t, "ut"),
		}
		if rapid.Bool().Draw(t, "haslang") {
			o["language"] = langGen.Draw(t, "lang")
		}
		objs = append(objs, o)
		r.closed(p)
		r.required(p, "objective", "status", "details", "waivers", "baselined", "under_threshold")
		r.bad(p, "objective", badObjID...)
		r.bad(p, "language", badLang...)
		r.bad(p, "status", "skipped", 1)
		r.bad(p, "details", "x", []any{7})
		r.bad(p, "waivers", "x", []any{"x"})
		r.bad(p, "baselined", -1, 1.5)
		r.bad(p, "under_threshold", -1, 1.5)
	}
	doc := map[string]any{
		"version":            0,
		"commit":             shaGen.Draw(t, "commit"),
		"changed_from":       map[string]any{"ref": textGen.Draw(t, "ref"), "sha": shaGen.Draw(t, "base")},
		"date":               "2026-09-27",
		"catalog":            "v" + strconv.Itoa(rapid.IntRange(0, 9).Draw(t, "cat")),
		"changed_files":      rapid.IntRange(0, 50).Draw(t, "changed"),
		"tool_versions":      tv,
		"pinned_by":          pins,
		"problems":           probs,
		"expired_waivers":    expired,
		"removable_baseline": removable,
		"objectives":         objs,
	}
	r.root("x", []any{})
	r.closed("")
	r.required("", "version", "commit", "changed_from", "date", "catalog", "changed_files", "tool_versions", "pinned_by", "problems", "expired_waivers", "removable_baseline", "objectives")
	r.bad("", "version", 1, "0")
	r.bad("", "commit", badSHA...)
	r.closed("/changed_from")
	r.required("/changed_from", "ref", "sha")
	r.bad("/changed_from", "ref", badText...)
	r.bad("/changed_from", "sha", badSHA...)
	r.bad("", "changed_from", "x")
	r.bad("", "date", "2026-02-30", 20260927)
	r.bad("", "catalog", "0", "vx", 0)
	r.bad("", "changed_files", -1, 1.5, "1")
	r.bad("", "tool_versions", "x")
	r.bad("", "pinned_by", "x")
	r.bad("", "problems", "x")
	r.bad("", "expired_waivers", "x", []any{"x"})
	r.bad("", "removable_baseline", "x", []any{"x"})
	r.bad("", "objectives", "x", []any{"x"})
	return sample{doc, r.cs}
})

var generators = map[Kind]*rapid.Generator[sample]{
	Baseline:        baselineGen,
	Report:          reportGen,
	Snapshot:        snapshotGen,
	StopState:       stopStateGen,
	AdapterCache:    cacheGen,
	Manifest:        manifestGen,
	Catalog:         catalogGen,
	Waivers:         waiversGen,
	Provenance:      provenanceGen,
	AdapterDescribe: describeGen,
	AdapterClassify: classifyGen,
	AdapterRun:      runGen,
	AdapterTools:    toolsGen,
	Reviews:         reviewsGen,
	Pending:         pendingGen,
}

func deepCopy(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = deepCopy(e)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = deepCopy(e)
		}
		return out
	default:
		return v
	}
}

func apply(doc any, c corruption) any {
	doc = deepCopy(doc)
	for _, o := range c.ops {
		if o.at == "" && o.key == "" {
			doc = o.val
			continue
		}
		node := doc
		if o.at != "" {
			for tok := range strings.SplitSeq(o.at[1:], "/") {
				switch n := node.(type) {
				case map[string]any:
					node = n[tok]
				case []any:
					i, _ := strconv.Atoi(tok)
					node = n[i]
				}
			}
		}
		switch n := node.(type) {
		case map[string]any:
			if o.del {
				delete(n, o.key)
			} else {
				n[o.key] = o.val
			}
		case []any:
			i, _ := strconv.Atoi(o.key)
			n[i] = o.val
		}
	}
	return doc
}

func serialize(k Kind, doc any) []byte {
	data, err := json.Marshal(doc)
	if err != nil {
		panic(err)
	}
	if k == Provenance {
		data = append(data, '\n')
	}
	return data
}

func caughtAt(vs []Violation, want string) bool {
	for _, v := range vs {
		if v.Location == want || strings.HasPrefix(v.Location, want+"/") {
			return true
		}
	}
	return false
}

func checkSample(v validator, k Kind, s sample) []string {
	var problems []string
	vs, err := v.validate(k, serialize(k, s.doc))
	if err != nil || len(vs) > 0 {
		problems = append(problems, fmt.Sprintf("%s: generated valid document rejected: err=%v %v\n%s", k, err, vs, serialize(k, s.doc)))
	}
	for _, c := range s.cs {
		vs, err := v.validate(k, serialize(k, apply(s.doc, c)))
		if err != nil && c.want == "" {
			continue
		}
		if err != nil || !caughtAt(vs, c.want) {
			problems = append(problems, fmt.Sprintf("%s: corruption %q not caught at %q (err=%v, violations=%v)", k, c.desc, c.want, err, vs))
		}
	}
	return problems
}

func TestGeneratedDocumentsAndTheirCorruptions(t *testing.T) {
	v, err := compiled()
	if err != nil {
		t.Fatal(err)
	}
	for k, gen := range generators {
		t.Run(string(k), func(t *testing.T) {
			rapid.Check(t, func(t *rapid.T) {
				for _, p := range checkSample(v, k, gen.Draw(t, "sample")) {
					t.Fatal(p)
				}
			})
		})
	}
}
