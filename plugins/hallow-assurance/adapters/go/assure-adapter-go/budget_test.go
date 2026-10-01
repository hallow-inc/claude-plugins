package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"pgregory.net/rapid"

	ev "github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/evidence"
	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/schemas"
)

func numberedLines(from, to int) string {
	var b strings.Builder
	b.WriteString("package a\n")
	for i := from; i < to; i++ {
		fmt.Fprintf(&b, "var v%d = %d\n", i, i)
	}
	return b.String()
}

func tableTest(rows int) string {
	var b strings.Builder
	b.WriteString("package a\n\nimport \"testing\"\n\nfunc TestTable(t *testing.T) {\n\tfor _, c := range []int{\n")
	for i := range rows {
		fmt.Fprintf(&b, "\t\t%d,\n", i)
	}
	b.WriteString("\t} {\n\t\tt.Run(\"row\", func(t *testing.T) { _ = c })\n\t}\n}\n")
	return b.String()
}

func budgetOf(t *testing.T, dir string) map[string]ev.BudgetFile {
	t.Helper()
	out := t.TempDir()
	resp, stderr, code := runIn(t, dir, "run", "VER-TEST-BUDGET", "--changed-from", "HEAD", "--out", out)
	if code != 0 {
		t.Fatalf("run failed %d: %s", code, stderr)
	}
	data, err := os.ReadFile(filepath.Join(out, resp.Evidence[0].Path))
	if err != nil {
		t.Fatal(err)
	}
	if vs, err := schemas.Validate(schemas.TestBudget, data); err != nil || len(vs) > 0 {
		t.Fatalf("test-budget.json invalid: %v %v\n%s", err, vs, data)
	}
	files, err := ev.ParseTestBudget(data)
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]ev.BudgetFile{}
	for _, f := range files {
		m[f.Path] = f
	}
	return m
}

func TestBudgetScenarios(t *testing.T) {
	dir := committedModule(t, map[string]string{
		"a/rename_test.go": "package a\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {}\n",
		"a/table_test.go":  tableTest(2),
		"a/big.go":         numberedLines(0, 60),
	})
	writeFiles(t, dir, map[string]string{
		"a/rename_test.go": "package a\n\nimport \"testing\"\n\nfunc TestB(t *testing.T) {}\n",
		"a/table_test.go":  tableTest(12),
		"a/big.go":         numberedLines(0, 20) + "var extra1 = 1\nvar extra2 = 2\n",
		"a/new.go":         "package a\n\nvar N = 1\n",
		"a/new_test.go": "package a\n\nimport (\n\t\"os/exec\"\n\t\"testing\"\n)\n\n" +
			"func TestOne(t *testing.T) {\n\tt.Run(\"x\", func(t *testing.T) {})\n\tt.Run(\"y\", func(t *testing.T) {})\n}\n\n" +
			"func TestTwo(t *testing.T) {\n\tcmd := exec.Command(\"true\")\n\t_ = cmd.Run()\n}\n",
	})
	got := budgetOf(t, dir)
	want := map[string][2]any{
		"a/rename_test.go": {"test", 1},
		"a/table_test.go":  {"test", 0},
		"a/big.go":         {"source", 2},
		"a/new.go":         {"source", 3},
		"a/new_test.go":    {"test", 4},
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for p, w := range want {
		f := got[p]
		n := f.AddedCases
		if w[0] == "source" {
			n = f.ChangedLines
		}
		if f.Role != w[0] || n != w[1] {
			t.Errorf("%s: role %q count %d, want %v", p, f.Role, n, w)
		}
	}
}

type runKind int

const (
	direct runKind = iota
	nested
	zeroArg
	foreignIdent
)

func (k runKind) weight() int {
	switch k {
	case direct:
		return 1
	case nested:
		return 2
	}
	return 0
}

func (k runKind) src() string {
	switch k {
	case direct:
		return "\tt.Run(\"s\", func(t *testing.T) {})\n"
	case nested:
		return "\tt.Run(\"o\", func(tt *testing.T) {\n\t\ttt.Run(\"i\", func(t *testing.T) {})\n\t})\n"
	case zeroArg:
		return "\t_ = exec.Command(\"true\").Run()\n"
	}
	return "\tg.Run(\"s\", nil)\n"
}

func budgetSource(tests map[string][]runKind, order []string) []byte {
	var b strings.Builder
	b.WriteString("package p\n\nimport (\n\t\"os/exec\"\n\t\"testing\"\n)\n\nvar _ = exec.Command\n\ntype grp struct{}\n\nfunc (grp) Run(string, any) {}\n\n")
	for _, n := range order {
		if strings.HasPrefix(n, "Fuzz") {
			fmt.Fprintf(&b, "func %s(f *testing.F) {\n\tf.Add(1)\n}\n\n", n)
			continue
		}
		fmt.Fprintf(&b, "func %s(t *testing.T) {\n\tvar g grp\n\t_ = g\n", n)
		for _, k := range tests[n] {
			b.WriteString(k.src())
		}
		b.WriteString("}\n\n")
	}
	return []byte(b.String())
}

func TestAddedCasesEqualsNewNamesPlusRunSiteDeltas(t *testing.T) {
	pool := []string{"TestA", "TestB", "TestC", "FuzzD", "TestE"}
	kinds := rapid.SliceOfN(rapid.SampledFrom([]runKind{direct, nested, zeroArg, foreignIdent}), 0, 4)
	side := func(t *rapid.T, label string) (map[string][]runKind, []string) {
		m := map[string][]runKind{}
		var order []string
		for _, n := range pool {
			if rapid.Bool().Draw(t, label+" has "+n) {
				m[n] = kinds.Draw(t, label+" runs "+n)
				order = append(order, n)
			}
		}
		return m, order
	}
	weigh := func(ks []runKind) int {
		w := 0
		for _, k := range ks {
			w += k.weight()
		}
		return w
	}
	rapid.Check(t, func(t *rapid.T) {
		bm, bo := side(t, "base")
		hm, ho := side(t, "head")
		want := 0
		for n, hk := range hm {
			if strings.HasPrefix(n, "Fuzz") {
				hk = nil
			}
			bk, ok := bm[n]
			if strings.HasPrefix(n, "Fuzz") {
				bk = nil
			}
			if !ok {
				want++
			}
			want += max(0, weigh(hk)-weigh(bk))
		}
		var bsrc []byte
		if len(bo) > 0 || rapid.Bool().Draw(t, "base file exists") {
			bsrc = budgetSource(bm, bo)
		}
		b, err := testCases(bsrc)
		if err != nil {
			t.Fatal(err)
		}
		h, err := testCases(budgetSource(hm, ho))
		if err != nil {
			t.Fatal(err)
		}
		if got := addedCases(b, h); got != want {
			t.Fatalf("added_cases %d, want %d\nbase:\n%s\nhead:\n%s", got, want, budgetSource(bm, bo), budgetSource(hm, ho))
		}
	})
}
