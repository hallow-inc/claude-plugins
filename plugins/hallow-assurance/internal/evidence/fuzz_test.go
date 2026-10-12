package evidence

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/schemas"
)

func addFixtures(f *testing.F, dirs ...string) {
	f.Helper()
	n := 0
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			f.Fatal(err)
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			data, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				f.Fatal(err)
			}
			f.Add(data)
			n++
		}
	}
	if n == 0 {
		f.Fatalf("no seed fixtures under %v", dirs)
	}
}

func schemaFixtures(kind schemas.Kind) []string {
	root := filepath.Join("..", "..", "schemas", "testdata", string(kind))
	return []string{filepath.Join(root, "valid"), filepath.Join(root, "invalid")}
}

// countTestcases is the D2 oracle: it must not share code with ParseJUnit, so that a counting
// bug in the reader cannot also hide in the check.
func countTestcases(data []byte) (cases, failing int, err error) {
	type frame struct{ testcase, failing bool }
	var stack []frame
	dec := xml.NewDecoder(bytes.NewReader(data))
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return cases, failing, nil
		}
		if err != nil {
			return 0, 0, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if n := len(stack); n > 0 && stack[n-1].testcase && !stack[n-1].failing &&
				(t.Name.Local == "failure" || t.Name.Local == "error") {
				stack[n-1].failing = true
				failing++
			}
			isCase := t.Name.Local == "testcase"
			if isCase {
				cases++
			}
			stack = append(stack, frame{testcase: isCase})
		case xml.EndElement:
			stack = stack[:len(stack)-1]
		}
	}
}

func FuzzParseJUnit(f *testing.F) {
	for _, s := range []string{
		`<testsuite name="a"/><testsuite name="b"><testcase name="TestX" classname="p"><failure/></testcase></testsuite>`,
		`<testsuite name="a"><group><testcase name="x"><failure/></testcase></group></testsuite>`,
		`<testsuite failures="0"><testcase name="TestX" classname="p"><failure/></testcase></testsuite>`,
		`<testsuites><testsuite><testcase name="(run)" classname="example.com/m"><error message="go test did not run">go: go.mod requires go &gt;= 1.27.1</error></testcase></testsuite></testsuites>`,
		`<testsuites><testsuite name="p"><properties><property name="assure.source" value="a.go"/></properties></testsuite></testsuites>`,
		`<?xml version="1.0" encoding="UTF-8"?>` + "\n" + `<testsuites><testcase name="a"><skipped/></testcase><testsuite><testcase name="b"/></testsuite></testsuites>`,
		`<testsuite><testcase name="a"><failure message="m">t</failure>`,
	} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		got, err := ParseJUnit(data)
		if err != nil {
			return
		}
		cases, failing, err := countTestcases(data)
		if err != nil {
			t.Fatalf("reader accepted bytes the oracle cannot tokenize: %v\n%q", err, data)
		}
		if got.Cases != cases || len(got.Failing) != failing {
			t.Fatalf("reader read %d cases, %d failing; the input holds %d cases, %d failing\n%q",
				got.Cases, len(got.Failing), cases, failing, data)
		}
	})
}

func FuzzParseSARIF(f *testing.F) {
	addFixtures(f, schemaFixtures(schemas.AdapterLint)...)
	f.Fuzz(func(t *testing.T, data []byte) {
		if _, err := ParseSARIF(data); err != nil {
			return
		}
		if vs, err := schemas.Validate(schemas.AdapterLint, data); err != nil || len(vs) > 0 {
			t.Fatalf("reader accepted bytes the schema rejects: %v %v\n%q", vs, err, data)
		}
	})
}

func FuzzParseMutationReport(f *testing.F) {
	addFixtures(f, schemaFixtures(schemas.MutationReport)...)
	f.Add(repeatedKeyReport)
	f.Fuzz(func(t *testing.T, data []byte) {
		if _, err := ParseMutationReport(data); err != nil {
			return
		}
		if vs, err := schemas.Validate(schemas.MutationReport, data); err != nil || len(vs) > 0 {
			t.Fatalf("reader accepted bytes the schema rejects: %v %v\n%q", vs, err, data)
		}
	})
}
