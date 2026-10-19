package hallowassurance_test

import (
	"context"
	"os/exec"
	"path"
	"slices"
	"strings"
	"testing"
	"time"

	"pgregory.net/rapid"
)

const modulePath = "github.com/hallow-inc/claude-plugins/plugins/hallow-assurance"

func strayMains(module, listOutput string) []string {
	var stray []string
	for line := range strings.SplitSeq(strings.TrimSpace(listOutput), "\n") {
		name, importPath, ok := strings.Cut(line, " ")
		if !ok || name != "main" || importPath == module+"/cmd/assure" {
			continue
		}
		if ok, _ := path.Match(module+"/adapters/*/assure-adapter-*", importPath); ok {
			continue
		}
		stray = append(stray, importPath)
	}
	return stray
}

func replaceLines(gomod string) []int {
	var lines []int
	for i, line := range strings.Split(gomod, "\n") {
		code, _, _ := strings.Cut(line, "//")
		if f := strings.Fields(code); len(f) > 0 && (f[0] == "replace" || strings.HasPrefix(f[0], "replace(")) {
			lines = append(lines, i+1)
		}
	}
	return lines
}

func TestModuleInstallsOnlyAssureAndAdapters(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, "go", "list", "-f", "{{if .GoFiles}}{{.Name}} {{.ImportPath}}\n{{end}}", "./...").Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	if !strings.Contains(string(out), "main "+modulePath+"/cmd/assure\n") {
		t.Fatalf("go list does not show %s/cmd/assure as a main package:\n%s", modulePath, out)
	}
	for _, p := range strayMains(modulePath, string(out)) {
		t.Errorf("main package %s: go install %s/...@vX.Y.Z would install it too; only assure and adapters/*/assure-adapter-* may be main", p, modulePath)
	}
	if lines := replaceLines(string(read(t, "go.mod"))); len(lines) > 0 {
		t.Errorf("go.mod has replace directives at lines %v; go install …@version refuses a module with replace", lines)
	}
}

func TestStrayMainsFlagsExactlyTheExtraMainPackages(t *testing.T) {
	seg := rapid.StringMatching(`[a-z][a-z0-9]{0,6}`)
	rapid.Check(t, func(t *rapid.T) {
		allowed := []string{"main " + modulePath + "/cmd/assure"}
		for _, l := range rapid.SliceOfN(seg, 0, 3).Draw(t, "langs") {
			allowed = append(allowed, "main "+modulePath+"/adapters/"+l+"/assure-adapter-"+l)
		}
		var lines, want []string
		lines = append(lines, allowed...)
		for range rapid.IntRange(0, 4).Draw(t, "libs") {
			lines = append(lines, seg.Draw(t, "pkgName")+"x "+modulePath+"/"+seg.Draw(t, "dir"))
		}
		for range rapid.IntRange(0, 3).Draw(t, "strays") {
			p := modulePath + "/" + rapid.SampledFrom([]string{"internal/", "cmd/", "adapters/x/", "adapters/", ""}).Draw(t, "under") + seg.Draw(t, "stray")
			if p == modulePath+"/cmd/assure" {
				continue
			}
			lines = append(lines, "main "+p)
			want = append(want, p)
		}
		nested := modulePath + "/adapters/go/assure-adapter-go/tool"
		if rapid.Bool().Draw(t, "nested") {
			lines = append(lines, "main "+nested)
			want = append(want, nested)
		}
		order := rapid.Permutation(lines).Draw(t, "order")
		got := strayMains(modulePath, strings.Join(order, "\n")+"\n")
		slices.Sort(got)
		slices.Sort(want)
		if !slices.Equal(got, want) {
			t.Fatalf("go list lines %q: stray %v, want %v", order, got, want)
		}
	})
}

func TestReplaceLinesFindsAnyReplaceDirective(t *testing.T) {
	gomod := string(read(t, "go.mod"))
	if got := replaceLines(gomod); len(got) != 0 {
		t.Fatalf("real go.mod reports replace at %v", got)
	}
	base := strings.Split(gomod, "\n")
	rapid.Check(t, func(t *rapid.T) {
		at := rapid.IntRange(1, len(base)).Draw(t, "at")
		directive := rapid.SampledFrom([]string{
			"replace example.com/a => ../a",
			"replace example.com/a v1.0.0 => example.com/b v1.1.0",
			"\treplace example.com/a => ./a // local",
			"replace (\n\texample.com/a => ../a\n)",
			"replace(\n\texample.com/a => ../a\n)",
		}).Draw(t, "directive")
		lines := slices.Insert(slices.Clone(base), at, directive)
		got := replaceLines(strings.Join(lines, "\n"))
		if !slices.Contains(got, at+1) {
			t.Fatalf("replace inserted at line %d not found (got %v) in:\n%s", at+1, got, strings.Join(lines, "\n"))
		}
		commented := slices.Insert(slices.Clone(base), at, "// replace example.com/a => ../a")
		if got := replaceLines(strings.Join(commented, "\n")); len(got) != 0 {
			t.Fatalf("commented-out replace reported at %v", got)
		}
	})
}
