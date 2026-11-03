package hallowassurance_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/core"
	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/decode"
	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/schemas"
)

func read(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func mustValidate(t *testing.T, k schemas.Kind, name string, data []byte) {
	t.Helper()
	vs, err := schemas.Validate(k, data)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	for _, v := range vs {
		t.Errorf("%s: %s", name, v)
	}
}

func charterSection(t *testing.T, heading string) string {
	t.Helper()
	charter := string(read(t, "CHARTER.md"))
	start := strings.Index(charter, heading)
	if start < 0 {
		t.Fatalf("CHARTER.md has no %q section", heading)
	}
	rest := charter[start+len(heading):]
	if end := strings.Index(rest, "\n## "); end >= 0 {
		rest = rest[:end]
	}
	return rest
}

var tableID = regexp.MustCompile(`(?m)^\| ([A-Z]+(?:-[A-Z0-9]+)+) \|`)

func TestCatalogMatchesCharterStarterTable(t *testing.T) {
	data := read(t, "catalog/v0/objectives.yaml")
	mustValidate(t, schemas.Catalog, "objectives.yaml", data)

	var charterIDs []string
	for _, m := range tableID.FindAllStringSubmatch(charterSection(t, "## Starter catalog"), -1) {
		charterIDs = append(charterIDs, m[1])
	}
	doc, err := decode.YAML(data)
	if err != nil {
		t.Fatal(err)
	}
	var catalogIDs []string
	for _, o := range doc.Value.([]any) {
		catalogIDs = append(catalogIDs, o.(map[string]any)["id"].(string))
	}
	slices.Sort(charterIDs)
	slices.Sort(catalogIDs)
	if len(charterIDs) == 0 || !slices.Equal(charterIDs, catalogIDs) {
		t.Fatalf("catalog IDs %v\ncharter IDs %v", catalogIDs, charterIDs)
	}
}

func TestFormalAndSimulationObjectivesStayAdvisory(t *testing.T) {
	doc, err := decode.YAML(read(t, "catalog/v0/objectives.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range doc.Value.([]any) {
		obj := o.(map[string]any)
		id := obj["id"].(string)
		var family string
		switch {
		case strings.HasPrefix(id, "FM-"):
			family = "formal"
		case strings.HasPrefix(id, "VER-DST-"):
			t.Errorf("%s: no adapter produces simulation evidence, so the catalog must not carry it", id)
			continue
		default:
			continue
		}
		if obj["applies_to"] != family {
			t.Errorf("%s: applies_to = %v, want %s", id, obj["applies_to"], family)
		}
		for level, status := range obj["levels"].(map[string]any) {
			if status != "advisory" {
				t.Errorf("%s: level %s is %v; v1 keeps this family advisory", id, level, status)
			}
		}
	}
}

func TestDogfoodManifestIsLevelB(t *testing.T) {
	data := read(t, "assurance.yaml")
	mustValidate(t, schemas.Manifest, "assurance.yaml", data)
	doc, err := decode.YAML(data)
	if err != nil {
		t.Fatal(err)
	}
	m := doc.Value.(map[string]any)
	if got := m["default_level"]; got != "B" {
		t.Fatalf("default_level = %v; the charter requires this repo to apply the framework to itself at level B", got)
	}
	if !slices.Contains(m["languages"].([]any), any("go")) {
		t.Fatalf("languages = %v; without go, no adapter classifies this repo's own code", m["languages"])
	}
	if _, err := core.LoadManifest("assurance.yaml"); err != nil {
		t.Fatal(err)
	}
}

var charterExample = regexp.MustCompile("(?s)\\*\\*(Manifest|Catalog objective|Waiver)\\*\\*[^\\n]*\\n(?:[^\\n]+\\n)*\\n```(?:yaml|json)\\n(.*?)```")

func TestCharterExamplesValidate(t *testing.T) {
	kinds := map[string]schemas.Kind{
		"Manifest":          schemas.Manifest,
		"Catalog objective": schemas.Catalog,
		"Waiver":            schemas.Waivers,
	}
	found := map[string]bool{}
	for _, m := range charterExample.FindAllStringSubmatch(charterSection(t, "## File formats"), -1) {
		found[m[1]] = true
		mustValidate(t, kinds[m[1]], "CHARTER.md "+m[1]+" example", []byte(m[2]))
	}
	for label := range kinds {
		if !found[label] {
			t.Errorf("no %s example found in CHARTER.md", label)
		}
	}
}

func scan(t *testing.T, root string, keep func(path string) bool, re *regexp.Regexp) []string {
	t.Helper()
	var hits []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "testdata" || d.Name() == "__to_delete" || d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !keep(p) {
			return nil
		}
		for i, line := range strings.Split(string(read(t, p)), "\n") {
			if re.MatchString(line) {
				hits = append(hits, p+":"+strconv.Itoa(i+1)+": "+strings.TrimSpace(line))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return hits
}

var hookFields = regexp.MustCompile(`hook_event_name|stop_hook_active|hookSpecificOutput|permissionDecision|"tool_input"|additionalContext`)

func TestOnlyHookioKnowsHookJSON(t *testing.T) {
	outside := func(p string) bool {
		return strings.HasSuffix(p, ".go") && !strings.HasSuffix(p, "_test.go") && !strings.HasPrefix(filepath.ToSlash(p), "internal/hookio/")
	}
	for _, hit := range scan(t, ".", outside, hookFields) {
		t.Errorf("hook JSON field outside internal/hookio (invariant: only hookio parses hook JSON): %s", hit)
	}
}

var languageNames = regexp.MustCompile(`golangci|gremlins|\bgo (test|vet|build)\b|_test\.go|\.go"|"go"|gofmt|typescript|stryker`)

func TestNoLanguageKnowledgeOutsideAdapters(t *testing.T) {
	for _, dir := range []string{"internal/core", "internal/hookio", "internal/app", "plugin", "plugin/skills"} {
		keep := func(p string) bool { return dir == "plugin" || !strings.HasSuffix(p, "_test.go") }
		if _, err := os.Stat(dir); err != nil {
			t.Errorf("%s: %v", dir, err)
			continue
		}
		for _, hit := range scan(t, dir, keep, languageNames) {
			t.Errorf("language-specific name outside adapters/ (design invariant 1): %s", hit)
		}
	}
}

var gremlinsFormat = regexp.MustCompile(`(?i)gremlins|"LIVED"|"NOT COVERED"|"TIMED OUT"|file_name`)

func TestEvidenceReadsOnlyStandardMutationFormat(t *testing.T) {
	keep := func(p string) bool { return !strings.HasSuffix(p, "_test.go") }
	for _, dir := range []string{"internal/evidence", "internal/core", "internal/app"} {
		for _, hit := range scan(t, dir, keep, gremlinsFormat) {
			t.Errorf("Gremlins' own output format outside the Go adapter (charter invariant 10: Stryker report only): %s", hit)
		}
	}
}

var sharedSetupSteps = []string{
	"Setup Go",
	"Let go.mod choose the toolchain",
	"Install assure",
	"Setup mise",
	"Install pinned tools",
}

func workflowSteps(t *testing.T, path string) (names []string, byName map[string]map[string]any) {
	t.Helper()
	var wf struct {
		Jobs map[string]struct {
			Steps []map[string]any `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(read(t, path), &wf); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	if len(wf.Jobs) != 1 {
		t.Fatalf("%s: want exactly one job, got %d", path, len(wf.Jobs))
	}
	byName = map[string]map[string]any{}
	for _, job := range wf.Jobs {
		for _, step := range job.Steps {
			name, _ := step["name"].(string)
			if _, dup := byName[name]; dup {
				t.Fatalf("%s: step name %q appears twice", path, name)
			}
			names = append(names, name)
			byName[name] = step
		}
	}
	return names, byName
}

func TestAuditWorkflowSetupMatchesEvaluate(t *testing.T) {
	const why = "assure-audit.yml copies its tool setup from assure-evaluate.yml (design D12); " +
		"a fix applied to one copy and not the other makes scheduled audits run a different assure or different tools from the PR gate"
	dir := filepath.Join("..", "..", ".github", "workflows")
	evalPath := filepath.Join(dir, "assure-evaluate.yml")
	auditPath := filepath.Join(dir, "assure-audit.yml")
	evalNames, evalSteps := workflowSteps(t, evalPath)
	auditNames, auditSteps := workflowSteps(t, auditPath)

	for _, name := range sharedSetupSteps {
		e, inEval := evalSteps[name]
		a, inAudit := auditSteps[name]
		if !inEval || !inAudit {
			t.Errorf("step %q: in evaluate=%v, in audit=%v; %s", name, inEval, inAudit, why)
			continue
		}
		if !reflect.DeepEqual(e, a) {
			eb, _ := yaml.Marshal(e)
			ab, _ := yaml.Marshal(a)
			t.Errorf("step %q differs; %s\n--- assure-evaluate.yml\n%s--- assure-audit.yml\n%s", name, why, eb, ab)
		}
	}

	shared := func(names []string) []string {
		return slices.DeleteFunc(slices.Clone(names), func(n string) bool { return !slices.Contains(sharedSetupSteps, n) })
	}
	if e, a := shared(evalNames), shared(auditNames); !slices.Equal(e, a) {
		t.Errorf("shared setup steps run in a different order: evaluate %q, audit %q; %s", e, a, why)
	}
}
