package schemas

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
	"golang.org/x/text/language"
	"golang.org/x/text/message"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/decode"
)

//go:embed *.schema.json external/*.json
var files embed.FS

type Kind string

const (
	Manifest        Kind = "manifest"
	Catalog         Kind = "catalog"
	Waivers         Kind = "waivers"
	Provenance      Kind = "provenance"
	AdapterDescribe Kind = "adapter-describe"
	AdapterClassify Kind = "adapter-classify"
	AdapterRun      Kind = "adapter-run"
	AdapterTools    Kind = "adapter-tools"
	AdapterLint     Kind = "adapter-lint"
	AdapterCache    Kind = "adapter-cache"
	Snapshot        Kind = "snapshot"
	StopState       Kind = "stop-state"
	Baseline        Kind = "baseline"
	Report          Kind = "report"
	MutationReport  Kind = "mutation-report"
	TestBudget      Kind = "test-budget"
	Resolutions     Kind = "coverage-resolutions"
	Reviews         Kind = "reviews"
	Pending         Kind = "pending"
)

const base = "https://assure.invalid/schemas/v0/"

const sarifID = "https://docs.oasis-open.org/sarif/sarif/v2.1.0/errata01/os/schemas/sarif-schema-2.1.0.json"

const strykerID = "http://stryker-mutator.io/report.schema.json"

type syntax int

const (
	yamlDoc syntax = iota
	jsonDoc
	jsonLines
)

var kinds = map[Kind]struct {
	url    string
	syntax syntax
}{
	Manifest:        {base + "manifest.schema.json", yamlDoc},
	Catalog:         {base + "catalog.schema.json", yamlDoc},
	Waivers:         {base + "waivers.schema.json", yamlDoc},
	Provenance:      {base + "provenance.schema.json", jsonLines},
	AdapterDescribe: {base + "adapter-describe.schema.json", jsonDoc},
	AdapterClassify: {base + "adapter-classify.schema.json", jsonDoc},
	AdapterRun:      {base + "adapter-run.schema.json", jsonDoc},
	AdapterTools:    {base + "adapter-tools.schema.json", jsonDoc},
	AdapterLint:     {sarifID, jsonDoc},
	AdapterCache:    {base + "adapter-cache.schema.json", jsonDoc},
	Snapshot:        {base + "snapshot.schema.json", jsonDoc},
	StopState:       {base + "stop-state.schema.json", jsonDoc},
	Baseline:        {base + "baseline.schema.json", jsonDoc},
	Report:          {base + "report.schema.json", jsonDoc},
	MutationReport:  {strykerID, jsonDoc},
	TestBudget:      {base + "test-budget.schema.json", jsonDoc},
	Resolutions:     {base + "coverage-resolutions.schema.json", yamlDoc},
	Reviews:         {base + "reviews.schema.json", jsonDoc},
	Pending:         {base + "pending.schema.json", jsonDoc},
}

type Violation struct {
	Line     int
	Column   int
	Location string
	Keyword  string
	Message  string
}

func (v Violation) String() string {
	loc := v.Location
	if loc == "" {
		loc = "/"
	}
	if v.Line > 0 {
		return fmt.Sprintf("line %d, column %d: %s: %s (%s)", v.Line, v.Column, loc, v.Message, v.Keyword)
	}
	return fmt.Sprintf("%s: %s (%s)", loc, v.Message, v.Keyword)
}

type refusingLoader struct{}

func (refusingLoader) Load(url string) (any, error) {
	return nil, fmt.Errorf("schema %s is not embedded in assure; refusing to load it", url)
}

func embedded() (map[string][]byte, error) {
	out := map[string][]byte{}
	err := fs.WalkDir(files, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := files.ReadFile(path)
		out[path] = data
		return err
	})
	return out, err
}

func compile(srcs map[string][]byte) (validator, error) {
	c := jsonschema.NewCompiler()
	c.UseLoader(refusingLoader{})
	c.AssertFormat()
	for path, data := range srcs {
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		id, err := schemaID(doc)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		if err := c.AddResource(id, doc); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
	}
	out := make(validator, len(kinds))
	for k, spec := range kinds {
		s, err := c.Compile(spec.url)
		if err != nil {
			return nil, fmt.Errorf("compile %s: %w", k, err)
		}
		out[k] = s
	}
	return out, nil
}

func schemaID(doc any) (string, error) {
	obj, ok := doc.(map[string]any)
	if !ok {
		return "", errors.New("schema is not an object")
	}
	for _, key := range []string{"$id", "id"} {
		if id, ok := obj[key].(string); ok && id != "" {
			return id, nil
		}
	}
	return "", errors.New("schema has no $id")
}

type validator map[Kind]*jsonschema.Schema

var compiled = sync.OnceValues(func() (validator, error) {
	srcs, err := embedded()
	if err != nil {
		return nil, err
	}
	return compile(srcs)
})

func Validate(k Kind, data []byte) ([]Violation, error) {
	v, err := compiled()
	if err != nil {
		return nil, err
	}
	return v.validate(k, data)
}

func (v validator) validate(k Kind, data []byte) ([]Violation, error) {
	spec, ok := kinds[k]
	if !ok {
		return nil, fmt.Errorf("unknown document kind %q", k)
	}
	sch := v[k]
	switch spec.syntax {
	case yamlDoc:
		doc, err := decode.YAML(data)
		if err != nil {
			return nil, err
		}
		return violations(sch, doc, 0), nil
	case jsonDoc:
		doc, err := decode.JSON(data)
		if err != nil {
			return nil, err
		}
		return violations(sch, doc, 0), nil
	default:
		docs, err := decode.JSONLines(data)
		if err != nil {
			return nil, err
		}
		var out []Violation
		for i, doc := range docs {
			out = append(out, violations(sch, doc, i+1)...)
		}
		return out, nil
	}
}

var printer = message.NewPrinter(language.English)

func violations(sch *jsonschema.Schema, doc decode.Doc, line int) []Violation {
	err := sch.Validate(doc.Value)
	if err == nil {
		return nil
	}
	var ve *jsonschema.ValidationError
	if !errors.As(err, &ve) {
		return []Violation{{Line: line, Keyword: "internal", Message: err.Error()}}
	}
	var out []Violation
	var leaves func(e *jsonschema.ValidationError)
	leaves = func(e *jsonschema.ValidationError) {
		if len(e.Causes) > 0 {
			for _, c := range e.Causes {
				leaves(c)
			}
			return
		}
		v := Violation{
			Location: pointer(e.InstanceLocation),
			Keyword:  keyword(e.ErrorKind),
			Message:  e.ErrorKind.LocalizedString(printer),
			Line:     line,
		}
		if line > 0 {
			v.Column = 1
		} else if p, ok := doc.Pos[v.Location]; ok {
			v.Line, v.Column = p.Line, p.Column
		}
		out = append(out, v)
	}
	leaves(ve)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Line != out[j].Line {
			return out[i].Line < out[j].Line
		}
		return out[i].Location < out[j].Location
	})
	return out
}

func keyword(k jsonschema.ErrorKind) string {
	if _, ok := k.(*kind.Not); ok {
		return "not"
	}
	return strings.Join(k.KeywordPath(), "/")
}

func pointer(tokens []string) string {
	var b strings.Builder
	for _, t := range tokens {
		b.WriteByte('/')
		b.WriteString(strings.ReplaceAll(strings.ReplaceAll(t, "~", "~0"), "/", "~1"))
	}
	return b.String()
}
