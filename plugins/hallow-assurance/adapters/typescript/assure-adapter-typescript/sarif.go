package main

import (
	"bufio"
	"bytes"
	"encoding/json"
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
	Locations  []sarifLocation  `json:"locations,omitempty"`
	Properties *sarifProperties `json:"properties,omitempty"`
}

type sarifProperties struct {
	Metric *int `json:"metric,omitempty"`
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

type eslintMessage struct {
	RuleID   *string `json:"ruleId"`
	Severity int     `json:"severity"`
	Message  string  `json:"message"`
	Fatal    bool    `json:"fatal"`
}

type eslintFile struct {
	FilePath string          `json:"filePath"`
	Messages []eslintMessage `json:"messages"`
}

func eslintResult(uri string, m eslintMessage) sarifResult {
	if m.Fatal {
		return result("parse", "error", m.Message, uri)
	}
	rule := "eslint"
	if m.RuleID != nil {
		rule = *m.RuleID
	}
	level := "warning"
	if m.Severity == 2 {
		level = "error"
	}
	return result(rule, level, m.Message, uri)
}

func eslintResults(root, unit string, stdout, stderr []byte, ok bool) []sarifResult {
	var files []eslintFile
	if !ok || json.Unmarshal(stdout, &files) != nil {
		return []sarifResult{result("eslint", "error", "eslint failed in "+unit+": "+capText(stderr), "")}
	}
	out := []sarifResult{}
	for _, f := range files {
		uri := relTo(root, f.FilePath)
		for _, m := range f.Messages {
			out = append(out, eslintResult(uri, m))
		}
	}
	return out
}

var (
	tscLine   = regexp.MustCompile(`^(.+)\(\d+,\d+\): (error|warning|message) (TS\d+): (.*)$`)
	tscGlobal = regexp.MustCompile(`^(error|warning|message) (TS\d+): (.*)$`)
)

var tscLevels = map[string]string{"error": "error", "warning": "warning", "message": "note"}

func tscResults(root, unit, name string, output []byte, failed bool) []sarifResult {
	out := []sarifResult{}
	sc := bufio.NewScanner(bytes.NewReader(output))
	sc.Buffer(make([]byte, 64<<10), 16<<20)
	for sc.Scan() {
		line := sc.Text()
		if m := tscLine.FindStringSubmatch(line); m != nil {
			out = append(out, result(m[3], tscLevels[m[2]], m[4], relTo(root, inUnit(unit, m[1]))))
		} else if m := tscGlobal.FindStringSubmatch(line); m != nil {
			out = append(out, result(m[2], tscLevels[m[1]], m[3], ""))
		} else if strings.HasPrefix(line, " ") && len(out) > 0 {
			out[len(out)-1].Message.Text += "\n" + strings.TrimSpace(line)
		}
	}
	if sc.Err() != nil || (failed && len(out) == 0) {
		out = append(out, result("tsc", "error", "tsc failed in "+name+": "+capText(output), ""))
	}
	return out
}

func inUnit(unit, p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(unit, filepath.FromSlash(p))
}
