package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

func onPath(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func realPath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

func configCandidates(rootRel string) []string {
	var configs []string
	for dir := rootRel; ; dir = path.Dir(dir) {
		if dir == "." || dir == "/" {
			dir = ""
		}
		for _, n := range miseConfigNames {
			configs = append(configs, path.Join(dir, n))
		}
		if dir == "" {
			return configs
		}
	}
}

func gatherProbe(root string) (probe, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return probe{}, err
	}
	top, err := toolOutput(root, "git", "rev-parse", "--show-toplevel")
	if err != nil {
		return probe{}, err
	}
	top = filepath.ToSlash(realPath(top))
	abs := filepath.ToSlash(realPath(root))
	rootRel, inside := relToTop(top, abs)
	if !inside && path.Clean(abs) != path.Clean(top) {
		return probe{}, fmt.Errorf("%s is not inside the git repository %s", root, top)
	}
	configs := configCandidates(rootRel)
	listed, err := trackedAmong(top, configs)
	if err != nil {
		return probe{}, err
	}
	p := probe{top: top, tracked: map[string]bool{}}
	for _, c := range configs {
		if listed[c] {
			p.trackedConfigs = append(p.trackedConfigs, c)
		}
	}
	return p, readMise(root, &p)
}

func readMise(root string, p *probe) error {
	if p.miseAbsent = !onPath("mise"); p.miseAbsent {
		return nil
	}
	stdout, stderr, code, err := runTool(root, "mise", "ls", "--json", "--local")
	if err != nil {
		return err
	}
	if code != 0 {
		p.miseErr = fmt.Sprintf("mise ls --json --local failed in %s: %s", root, capText(stderr))
		return nil
	}
	if err := json.Unmarshal(stdout, &p.mise); err != nil {
		p.miseErr = "mise ls --json --local printed unreadable output: " + err.Error()
	}
	if p.miseErr != "" {
		return nil
	}
	var sources []string
	for _, entries := range p.mise {
		for i := range entries {
			if entries[i].Source.Path == "" {
				continue
			}
			entries[i].Source.Path = filepath.ToSlash(realPath(entries[i].Source.Path))
			if rel, ok := relToTop(p.top, entries[i].Source.Path); ok {
				sources = append(sources, rel)
			}
		}
	}
	p.tracked, err = trackedAmong(p.top, sources)
	return err
}

func trackedAmong(top string, rels []string) (map[string]bool, error) {
	out := map[string]bool{}
	if len(rels) == 0 {
		return out, nil
	}
	lines, err := gitLines(top, append([]string{"ls-files", "--"}, rels...)...)
	if err != nil {
		return nil, err
	}
	for _, l := range lines {
		out[l] = true
	}
	return out, nil
}

func foundVersion(name string) (string, error) {
	if _, err := exec.LookPath(name); err != nil {
		return "", fmt.Errorf("%s is not on PATH", name)
	}
	switch name {
	case "gremlins":
		return gremlinsVersion()
	default:
		return toolOutput(".", name, "version", "--short")
	}
}

func checkFound(r resolved) string {
	found, err := foundVersion(r.name)
	switch {
	case err != nil:
		return fmt.Sprintf("%s: %v; pinned %s by %s; install it with: %s", r.name, err, r.version, r.pinnedBy, r.install)
	case !sameVersion(r.version, found):
		return fmt.Sprintf("%s: found %s on PATH, pinned %s by %s; install the pinned version with: %s", r.name, found, r.version, r.pinnedBy, r.install)
	}
	return ""
}

func toolsFor(root, obj string) (versions, pinnedBy map[string]string, environment string, err error) {
	versions, pinnedBy = map[string]string{}, map[string]string{}
	names := objectiveTools[obj]
	if len(names) == 0 {
		return versions, pinnedBy, "", nil
	}
	p, err := gatherProbe(root)
	if err != nil {
		return nil, nil, "", err
	}
	var problems []string
	for _, r := range resolve(p) {
		if !slices.Contains(names, r.tool.name) {
			continue
		}
		if r.problem != "" {
			problems = append(problems, r.problem)
			continue
		}
		if msg := checkFound(r.res); msg != "" {
			problems = append(problems, msg)
			continue
		}
		versions[r.res.name] = r.res.version
		pinnedBy[r.res.name] = r.res.pinnedBy
	}
	if len(problems) > 0 {
		return nil, nil, strings.Join(problems, "\n"), nil
	}
	return versions, pinnedBy, "", nil
}

type toolEntry struct {
	Name     string `json:"name"`
	Version  string `json:"version"`
	PinnedBy string `json:"pinned_by"`
	Install  string `json:"install"`
}

type toolsResponse struct {
	Protocol    int                 `json:"protocol"`
	Tools       []toolEntry         `json:"tools"`
	Environment *environmentMessage `json:"environment,omitempty"`
}

func listTools(root string) (toolsResponse, error) {
	p, err := gatherProbe(root)
	if err != nil {
		return toolsResponse{}, err
	}
	resp := toolsResponse{Protocol: 1, Tools: []toolEntry{}}
	var problems []string
	for _, r := range resolve(p) {
		if r.problem != "" {
			problems = append(problems, r.problem)
			continue
		}
		resp.Tools = append(resp.Tools, toolEntry{Name: r.res.name, Version: r.res.version, PinnedBy: r.res.pinnedBy, Install: r.res.install})
	}
	if len(problems) > 0 {
		resp.Tools = []toolEntry{}
		resp.Environment = &environmentMessage{Message: strings.Join(problems, "\n")}
	}
	slices.SortFunc(resp.Tools, func(a, b toolEntry) int { return strings.Compare(a.Name, b.Name) })
	return resp, nil
}

func toolsOp(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("tools", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", "", "repository directory to resolve tools for")
	if err := fs.Parse(args); err != nil || *root == "" || fs.NArg() > 0 {
		_, _ = fmt.Fprintln(stderr, "usage: assure-adapter-go tools --root <dir>")
		return 2
	}
	resp, err := listTools(*root)
	if err == nil {
		err = json.NewEncoder(stdout).Encode(resp)
	}
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "assure-adapter-go: tools: %v\n", err)
		return 1
	}
	return 0
}
