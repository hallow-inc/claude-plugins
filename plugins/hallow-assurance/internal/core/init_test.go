package core

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

var initLangGen = rapid.OneOf(
	rapid.StringMatching(`[a-z][a-z0-9_-]{0,10}`),
	rapid.SampledFrom([]string{"go", "typescript", "python", "null", "true", "false"}),
)

func uncommentExamples(tmpl string) string {
	lines := strings.Split(tmpl, "\n")
	after := false
	for i, l := range lines {
		switch {
		case l == "components: []":
			lines[i] = "components:"
			after = true
		case after && strings.HasPrefix(l, "#  "):
			lines[i] = l[1:]
		case after && strings.HasPrefix(l, "# require_fix_test:"):
			lines[i] = l[2:]
		}
	}
	return strings.Join(lines, "\n")
}

func TestInitTemplateIsAValidManifestAsPrintedAndUncommented(t *testing.T) {
	cat, err := LoadCatalog(CurrentCatalog)
	if err != nil {
		t.Fatal(err)
	}
	rapid.Check(t, func(t *rapid.T) {
		langs := rapid.SliceOfNDistinct(initLangGen, 1, 4, func(s string) string { return s }).Draw(t, "langs")
		tmpl := RenderInit(cat, langs)
		m, err := ParseManifest(ManifestName, []byte(tmpl))
		if err != nil {
			t.Fatalf("langs %q: assure init printed a manifest assure rejects: %v\n%s", langs, err, tmpl)
		}
		if !slices.Equal(m.Languages, langs) || m.Catalog.Version != cat.Version || len(m.Components) != 0 || m.RequireFixTest {
			t.Fatalf("langs %q: template parsed to languages %q catalog %q components %d require_fix_test %v", langs, m.Languages, m.Catalog.Version, len(m.Components), m.RequireFixTest)
		}
		edited := uncommentExamples(tmpl)
		if edited == tmpl {
			t.Fatal("no commented example found to uncomment")
		}
		m, err = ParseManifest(ManifestName, []byte(edited))
		if err != nil {
			t.Fatalf("langs %q: uncommenting the examples users are told to uncomment breaks the manifest: %v\n%s", langs, err, edited)
		}
		if len(m.Components) == 0 || m.RequireFixTest {
			t.Fatalf("uncommented examples parsed to %d components, require_fix_test %v; the examples must show real components and the default", len(m.Components), m.RequireFixTest)
		}
	})
}

func TestInitTemplateWithoutAdaptersSaysSoAndListsNone(t *testing.T) {
	cat, err := LoadCatalog(CurrentCatalog)
	if err != nil {
		t.Fatal(err)
	}
	empty := RenderInit(cat, nil)
	if !strings.Contains(empty, "\nlanguages: []\n") || strings.Contains(empty, "Adapters found on PATH:") {
		t.Fatalf("no adapters must render languages: [] without claiming any were found:\n%s", empty)
	}
	rapid.Check(t, func(t *rapid.T) {
		langs := rapid.SliceOfNDistinct(rapid.StringMatching(`[a-z][a-z0-9_-]{0,10}`), 1, 4, func(s string) string { return s }).Draw(t, "langs")
		tmpl := RenderInit(cat, langs)
		if !strings.Contains(tmpl, "Adapters found on PATH: "+strings.Join(langs, ", ")+".") || strings.Contains(tmpl, "No assure-adapter-* found") {
			t.Fatalf("langs %q: the comment must name the adapters found:\n%s", langs, tmpl)
		}
	})
}

type schemaNode = map[string]any

func loadSchema(t *testing.T, name string) schemaNode {
	t.Helper()
	data, err := os.ReadFile("../../schemas/" + name)
	if err != nil {
		t.Fatal(err)
	}
	var s schemaNode
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatal(err)
	}
	return s
}

func resolveRef(t *testing.T, node schemaNode, self, common schemaNode) schemaNode {
	t.Helper()
	ref, ok := node["$ref"].(string)
	if !ok {
		return node
	}
	doc := self
	if file, frag, found := strings.Cut(ref, "#"); found && file != "" {
		if file != "common.schema.json" {
			t.Fatalf("unhandled $ref %s", ref)
		}
		doc, ref = common, "#"+frag
	}
	parts := strings.Split(strings.TrimPrefix(ref, "#/"), "/")
	var cur any = doc
	for _, p := range parts {
		cur = cur.(schemaNode)[p]
	}
	return cur.(schemaNode)
}

func TestInitTemplateNamesEverySchemaKeyAndEnumValue(t *testing.T) {
	cat, err := LoadCatalog(CurrentCatalog)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := RenderInit(cat, []string{"go"})
	manifest := loadSchema(t, "manifest.schema.json")
	common := loadSchema(t, "common.schema.json")
	defs := manifest["$defs"].(schemaNode)
	scopes := map[string]schemaNode{"top level": manifest, "$defs.component": defs["component"].(schemaNode), "$defs.formal": defs["formal"].(schemaNode)}
	altGroup := regexp.MustCompile(`\w+(?: \| \w+)+`)
	checked := 0
	for scope, obj := range scopes {
		props := obj["properties"].(schemaNode)
		if len(props) == 0 {
			t.Fatalf("%s has no properties; schema layout changed", scope)
		}
		for name, raw := range props {
			keyLine := regexp.MustCompile(`(?m)^#?[ \t]*(?:- )?` + regexp.QuoteMeta(name) + `(?::| {2,}).*$`)
			lines := keyLine.FindAllString(tmpl, -1)
			if len(lines) == 0 {
				t.Errorf("%s property %q is not documented in the init template", scope, name)
				continue
			}
			checked++
			prop := resolveRef(t, raw.(schemaNode), manifest, common)
			if enum, ok := prop["enum"].([]any); ok {
				want := make([]string, len(enum))
				for i, v := range enum {
					want[i] = fmt.Sprint(v)
				}
				slices.Sort(want)
				if !slices.ContainsFunc(strings.Split(tmpl, "\n"), func(l string) bool {
					for _, g := range altGroup.FindAllString(l, -1) {
						got := strings.Split(g, " | ")
						slices.Sort(got)
						if slices.Equal(got, want) {
							return true
						}
					}
					return false
				}) {
					t.Errorf("%s property %q: no template line lists exactly its values %v as X | Y | ...", scope, name, want)
				}
			}
			if c, ok := prop["const"]; ok {
				val := regexp.MustCompile(`\b` + regexp.QuoteMeta(fmt.Sprint(c)) + `\b`)
				if !slices.ContainsFunc(lines, val.MatchString) {
					t.Errorf("%s property %q: its template line %q does not give the only value %v", scope, name, lines, c)
				}
			}
		}
	}
	if checked < 10 {
		t.Fatalf("only %d properties checked; schema read went wrong", checked)
	}
}

type initLine struct {
	status    string
	threshold string
	applies   string
}

var objectiveLine = regexp.MustCompile(`^#     (\S+) +(required|advisory)(?: ([0-9.e+-]+)%)?(?: \[(\w+)\])?$`)

func parseInitLevels(t interface {
	Helper()
	Fatalf(string, ...any)
}, tmpl string) map[Level]map[string]initLine {
	t.Helper()
	out := map[Level]map[string]initLine{}
	var cur Level
	inSection := false
	for _, l := range strings.Split(tmpl, "\n") {
		if strings.HasPrefix(l, "#   ") && len(l) == 6 && l[5] == ':' {
			cur = Level(l[4:5])
			if _, dup := out[cur]; dup {
				t.Fatalf("level %s listed twice", cur)
			}
			out[cur] = map[string]initLine{}
			inSection = true
			continue
		}
		if !inSection {
			continue
		}
		if !strings.HasPrefix(l, "#     ") {
			break
		}
		if l == "#     (none)" {
			if len(out[cur]) != 0 {
				t.Fatalf("level %s shows (none) after objectives", cur)
			}
			out[cur] = nil
			continue
		}
		m := objectiveLine.FindStringSubmatch(l)
		if m == nil {
			t.Fatalf("unparseable objective line %q", l)
		}
		if out[cur] == nil {
			t.Fatalf("level %s lists %q after (none)", cur, l)
		}
		if _, dup := out[cur][m[1]]; dup {
			t.Fatalf("level %s lists %s twice", cur, m[1])
		}
		out[cur][m[1]] = initLine{m[2], m[3], m[4]}
	}
	return out
}

func checkInitLevels(t interface {
	Helper()
	Fatalf(string, ...any)
}, c Catalog) {
	t.Helper()
	got := parseInitLevels(t, RenderInit(c, []string{"go"}))
	if len(got) != len(levels) {
		t.Fatalf("template lists levels %v, want all of %v", got, levels)
	}
	for _, l := range levels {
		want := wantInitLevel(c, l)
		sec, present := got[l]
		if !present {
			t.Fatalf("level %s missing from template", l)
		}
		if len(want) == 0 {
			if sec != nil {
				t.Fatalf("level %s has no objectives in the catalog; template must show (none), got %v", l, sec)
			}
			continue
		}
		if len(sec) != len(want) {
			t.Fatalf("level %s: template lists %v, catalog has %v", l, sec, want)
		}
		for id, w := range want {
			g, ok := sec[id]
			if !ok || !sameInitLine(g, w) {
				t.Fatalf("level %s %s: template says %+v (listed %v), catalog says %+v", l, id, g, ok, w)
			}
		}
	}
}

func wantInitLevel(c Catalog, l Level) map[string]initLine {
	want := map[string]initLine{}
	for _, o := range c.Objectives {
		s, ok := o.Levels[l]
		if !ok {
			continue
		}
		il := initLine{status: s, applies: o.AppliesTo}
		if th, ok := o.Threshold[l]; ok {
			il.threshold = strconv.FormatFloat(th, 'g', -1, 64)
		}
		want[o.ID] = il
	}
	return want
}

func sameInitLine(g, w initLine) bool {
	if g.status != w.status || g.applies != w.applies || (g.threshold == "") != (w.threshold == "") {
		return false
	}
	if w.threshold == "" {
		return true
	}
	gv, gerr := strconv.ParseFloat(g.threshold, 64)
	wv, werr := strconv.ParseFloat(w.threshold, 64)
	return gerr == nil && werr == nil && gv == wv
}

func TestInitLevelsMatchTheEmbeddedCatalog(t *testing.T) {
	cat, err := LoadCatalog(CurrentCatalog)
	if err != nil {
		t.Fatal(err)
	}
	checkInitLevels(t, cat)
}

func TestInitLevelsMatchAnyCatalog(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		ids := rapid.SliceOfNDistinct(rapid.StringMatching(`[A-Z]{2,4}-[A-Z0-9]{1,4}`), 1, 5, func(s string) string { return s }).Draw(t, "ids")
		var docs []map[string]any
		for _, id := range ids {
			lv := map[string]any{}
			th := map[string]any{}
			for _, l := range levels {
				s := rapid.SampledFrom([]string{"", "required", "advisory"}).Draw(t, "status"+string(l))
				if s == "" {
					continue
				}
				lv[string(l)] = s
				if rapid.Bool().Draw(t, "th"+string(l)) {
					th[string(l)] = rapid.SampledFrom([]float64{0, 1, 50, 65, 72.5, 99.9, 100}).Draw(t, "pct"+string(l))
				}
			}
			if len(lv) == 0 {
				lv[string(rapid.SampledFrom(levels).Draw(t, "only"))] = "advisory"
			}
			o := map[string]any{"id": id, "title": "t", "source": []any{"s"}, "levels": lv, "evidence": "e"}
			if len(th) > 0 {
				o["threshold"] = th
			}
			if a := rapid.SampledFrom([]string{"", "fix", "inputs", "formal"}).Draw(t, "applies"); a != "" {
				o["applies_to"] = a
			}
			docs = append(docs, o)
		}
		c, err := parseCatalog("objectives.yaml", mustJSON(docs))
		if err != nil {
			t.Fatalf("generator produced an invalid catalog: %v", err)
		}
		c.Version = "v9"
		checkInitLevels(t, c)
	})
}
