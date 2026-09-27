package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

type sarifLocation struct {
	PhysicalLocation struct {
		ArtifactLocation struct {
			URI string `json:"uri"`
		} `json:"artifactLocation"`
	} `json:"physicalLocation"`
}

type sarifResult struct {
	RuleID  string `json:"ruleId,omitempty"`
	Level   string `json:"level,omitempty"`
	Message struct {
		Text string `json:"text"`
	} `json:"message"`
	Locations []sarifLocation `json:"locations,omitempty"`
}

type sarifRun struct {
	Tool struct {
		Driver struct {
			Name string `json:"name"`
		} `json:"driver"`
	} `json:"tool"`
	Results []sarifResult `json:"results"`
}

type sarifLog struct {
	Version string     `json:"version"`
	Runs    []sarifRun `json:"runs"`
}

func newRun(name string) sarifRun {
	r := sarifRun{Results: []sarifResult{}}
	r.Tool.Driver.Name = name
	return r
}

func result(rule, level, msg, uri string) sarifResult {
	r := sarifResult{RuleID: rule, Level: level}
	r.Message.Text = msg
	if uri != "" {
		var l sarifLocation
		l.PhysicalLocation.ArtifactLocation.URI = uri
		r.Locations = []sarifLocation{l}
	}
	return r
}

var vetLine = regexp.MustCompile(`^(?:vet: )?(\S+?\.go):\d+:\d+: (.*)$`)

func repoRel(root, mod, p string) string {
	if !filepath.IsAbs(p) {
		return path.Clean(path.Join(mod, filepath.ToSlash(p)))
	}
	if real, err := filepath.EvalSymlinks(p); err == nil {
		p = real
	}
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return filepath.ToSlash(p)
	}
	return filepath.ToSlash(rel)
}

func vetResults(root, mod string, stderr []byte, failed bool) []sarifResult {
	var out []sarifResult
	sc := bufio.NewScanner(bytes.NewReader(stderr))
	for sc.Scan() {
		if m := vetLine.FindStringSubmatch(sc.Text()); m != nil {
			out = append(out, result("govet", "error", m[2], repoRel(root, mod, m[1])))
		}
	}
	if failed && len(out) == 0 {
		out = append(out, result("govet", "error", "go vet failed in "+mod+": "+capText(stderr), ""))
	}
	return out
}

func lintResults(root, mod, sarifFile string, stderr []byte, ok bool) []sarifResult {
	var log sarifLog
	data, err := os.ReadFile(sarifFile)
	if !ok || err != nil || json.Unmarshal(data, &log) != nil {
		return []sarifResult{result("golangci-lint", "error", "golangci-lint failed in "+mod+": "+capText(stderr), "")}
	}
	var out []sarifResult
	for _, run := range log.Runs {
		for _, r := range run.Results {
			for i := range r.Locations {
				u := &r.Locations[i].PhysicalLocation.ArtifactLocation.URI
				*u = repoRel(root, mod, strings.TrimPrefix(*u, "file://"))
			}
			out = append(out, r)
		}
	}
	return out
}

func capText(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > maxOutput {
		s = s[:maxOutput]
	}
	if s == "" {
		s = "no output"
	}
	return s
}
