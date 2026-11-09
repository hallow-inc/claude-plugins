package hallowassurance_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

const ghStub = `#!/usr/bin/env bash
set -euo pipefail
dir=$GH_STUB_DIR
n=$(( $(wc -l < "$dir/calls.jsonl") + 1 ))

flag() {
  local name=$1; shift
  while (($#)); do
    case $1 in
      "$name") echo "${2:-}"; return ;;
      "$name"=*) echo "${1#*=}"; return ;;
    esac
    shift
  done
}

body=$(flag --body-file "$@")
copy=""
if [[ -n $body ]]; then
  copy="$dir/body-$n"
  cp "$body" "$copy"
fi
jq -cn --arg body "$copy" '{args: $ARGS.positional, body: $body}' --args -- "$@" >> "$dir/calls.jsonl"

case "${1:-} ${2:-}" in
  "label create") ;;
  "issue list")
    jq -c --arg label "$(flag --label "$@")" --arg state "$(flag --state "$@")" '
      [.[] | select(($label == "" or (.labels | index($label) != null))
                    and (($state == "" and .state == "open") or $state == "all" or .state == $state))
           | {number, title, url}]' "$dir/issues.json" ;;
  "issue create") echo "https://github.com/hallow-inc/example/issues/$((100 + n))" ;;
  "issue comment") echo "https://github.com/hallow-inc/example/issues/$3#issuecomment-$n" ;;
  *) echo "gh stub: unexpected call: $*" >&2; exit 2 ;;
esac
`

type ghCall struct {
	Args     []string `json:"args"`
	BodyFile string   `json:"body"`
	Body     string   `json:"-"`
}

func (c ghCall) is(verb ...string) bool {
	return len(c.Args) >= len(verb) && slices.Equal(c.Args[:len(verb)], verb)
}

func (c ghCall) flag(name string) string {
	for i, a := range c.Args {
		if a == name && i+1 < len(c.Args) {
			return c.Args[i+1]
		}
		if v, ok := strings.CutPrefix(a, name+"="); ok {
			return v
		}
	}
	return ""
}

type nightlyRun struct {
	mod   string
	code  int
	out   string
	calls []ghCall
}

func (r nightlyRun) callsOf(verb ...string) []ghCall {
	var out []ghCall
	for _, c := range r.calls {
		if c.is(verb...) {
			out = append(out, c)
		}
	}
	return out
}

func runFuzzNightly(t *testing.T, files map[string]string, issues string, env ...string) nightlyRun {
	t.Helper()
	if testing.Short() {
		t.Skip("runs real go test -fuzz against a planted module")
	}
	script, err := filepath.Abs(filepath.Join("ci", "fuzz-nightly.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(script); err != nil {
		t.Fatalf("nightly fuzz script: %v", err)
	}

	mod := plantModule(t, files)
	stub := t.TempDir()
	for name, body := range map[string]string{"gh": ghStub, "issues.json": issues, "calls.jsonl": ""} {
		if err := os.WriteFile(filepath.Join(stub, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	cmd := exec.CommandContext(t.Context(), "bash", script)
	cmd.Dir = mod
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if name != "PATH" && name != "RUN_URL" && name != "FUZZTIME" && !strings.HasPrefix(name, "GH_") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	cmd.Env = append(cmd.Env, "PATH="+stub+string(os.PathListSeparator)+os.Getenv("PATH"), "GH_STUB_DIR="+stub)
	cmd.Env = append(cmd.Env, env...)
	out, err := cmd.CombinedOutput()
	r := nightlyRun{mod: mod, out: string(out)}
	if ee := (*exec.ExitError)(nil); errors.As(err, &ee) {
		r.code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("running %s: %v\n%s", script, err, out)
	}

	r.calls = recordedCalls(t, filepath.Join(stub, "calls.jsonl"))
	return r
}

// plantModule puts the module under mod/ in a fresh git repository so that a repo-relative
// corpus path differs from a module-relative one.
func plantModule(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	if out, err := exec.CommandContext(t.Context(), "git", "init", "-q", root).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	mod := filepath.Join(root, "mod")
	files["go.mod"] = "module example.com/planted\n\ngo 1.24\n"
	for name, body := range files {
		p := filepath.Join(mod, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return mod
}

func recordedCalls(t *testing.T, log string) []ghCall {
	t.Helper()
	var calls []ghCall
	for line := range strings.Lines(string(read(t, log))) {
		var c ghCall
		if err := json.Unmarshal([]byte(line), &c); err != nil {
			t.Fatalf("gh stub log: %v", err)
		}
		if c.BodyFile != "" {
			c.Body = string(read(t, c.BodyFile))
		}
		calls = append(calls, c)
	}
	return calls
}

// newInput returns the one failing input Go wrote for the target. It must still be on disk after
// the script: the workflow's artifact step uploads untracked files under testdata/fuzz/.
func newInput(t *testing.T, r nightlyRun, pkg, target string) (hash, contents string) {
	t.Helper()
	got, err := filepath.Glob(filepath.Join(r.mod, filepath.FromSlash(pkg), "testdata", "fuzz", target, "*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("%s %s: want one failing input left on disk, got %v\n%s", pkg, target, got, r.out)
	}
	return filepath.Base(got[0]), string(read(t, got[0]))
}

func crasherTitle(pkg, target string) string {
	return "Nightly fuzz crasher: " + pkg + " " + target
}

var fenceOpen = regexp.MustCompile("^(`{3,})[^`]*$")

func longestBacktickRun(s string) int {
	longest, run := 0, 0
	for _, c := range s {
		if c == '`' {
			run++
			longest = max(longest, run)
		} else {
			run = 0
		}
	}
	return longest
}

// fenced reports whether contents appears verbatim as the whole of a code block whose fence
// cannot be closed early by a backtick run inside contents.
func fenced(body, contents string) bool {
	idx := strings.Index(body, contents)
	if idx < 0 || !strings.HasSuffix(body[:idx], "\n") {
		return false
	}
	before := strings.TrimSuffix(body[:idx], "\n")
	m := fenceOpen.FindStringSubmatch(before[strings.LastIndex(before, "\n")+1:])
	if m == nil || len(m[1]) <= longestBacktickRun(contents) {
		return false
	}
	after := body[idx+len(contents):]
	if !strings.HasSuffix(contents, "\n") {
		var ok bool
		if after, ok = strings.CutPrefix(after, "\n"); !ok {
			return false
		}
	}
	closing, _, _ := strings.Cut(after, "\n")
	closing = strings.TrimRight(closing, " ")
	return len(closing) >= len(m[1]) && strings.Trim(closing, "`") == ""
}

// assertHandoff checks what /hallow-assurance:bugfix needs from the body: the command that
// replays the input, the path to commit it under, and the pointer to bugfix itself.
func assertHandoff(t *testing.T, body, pkg, target, hash string) {
	t.Helper()
	for _, want := range []string{
		fmt.Sprintf("go test -run='^%s$/^%s$' ./%s", target, hash, pkg),
		"mod/" + pkg + "/testdata/fuzz/" + target + "/" + hash,
		"/hallow-assurance:bugfix",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("%s %s body lacks %q:\n%s", pkg, target, want, body)
		}
	}
}

const anyMutationFails = `package x

import "testing"

func FuzzCrash(f *testing.F) {
	f.Add("ok")
	f.Fuzz(func(t *testing.T, s string) {
		if s != "ok" {
			t.Fatal("crash")
		}
	})
}
`

func TestFuzzNightlyOpensAnIssuePerTargetWithANewInput(t *testing.T) {
	r := runFuzzNightly(t, map[string]string{
		"internal/x/x.go":      "package x\n",
		"internal/x/x_test.go": anyMutationFails,
		"y/y.go":               "package y\n",
		"y/y_test.go": "package y\n\nimport (\n\t\"strings\"\n\t\"testing\"\n)\n\n" +
			"func FuzzBackticks(f *testing.F) {\n\tf.Add(\"````x\")\n\tf.Fuzz(func(t *testing.T, s string) {\n" +
			"\t\tif s != \"````x\" && strings.Contains(s, \"````\") {\n\t\t\tt.Fatal(\"backticks\")\n\t\t}\n\t})\n}\n" +
			"\nfunc FuzzSeedFails(f *testing.F) {\n\tf.Add(\"boom\")\n\tf.Fuzz(func(t *testing.T, s string) {\n" +
			"\t\tif s == \"boom\" {\n\t\t\tt.Fatal(\"seed\")\n\t\t}\n\t})\n}\n",
	}, "[]", "FUZZTIME=10s")

	if r.code == 0 {
		t.Errorf("a run with crashers exited 0, hiding them\n%s", r.out)
	}
	if len(r.callsOf("issue", "comment")) != 0 {
		t.Errorf("no open issue exists, yet the script commented: %v", r.callsOf("issue", "comment"))
	}

	creates := r.callsOf("issue", "create")
	byTitle := map[string]ghCall{}
	for _, c := range creates {
		if c.flag("--label") != "fuzz-crasher" {
			t.Errorf("issue %q lacks the fuzz-crasher label that dedup lists by: %v", c.flag("--title"), c.Args)
		}
		byTitle[c.flag("--title")] = c
	}
	want := []struct{ pkg, target string }{{"internal/x", "FuzzCrash"}, {"y", "FuzzBackticks"}}
	if len(creates) != len(want) || len(byTitle) != len(want) {
		t.Fatalf("want one issue per target with a new input (FuzzSeedFails wrote none), got %d creates: %v\n%s", len(creates), creates, r.out)
	}
	for _, w := range want {
		c, ok := byTitle[crasherTitle(w.pkg, w.target)]
		if !ok {
			t.Errorf("no issue titled %q; titles %v", crasherTitle(w.pkg, w.target), slices.Sorted(maps.Keys(byTitle)))
			continue
		}
		hash, contents := newInput(t, r, w.pkg, w.target)
		if !fenced(c.Body, contents) {
			t.Errorf("%s body does not hold the input %q in a fence it cannot break:\n%s", w.target, contents, c.Body)
		}
		assertHandoff(t, c.Body, w.pkg, w.target, hash)
	}

	firstCreate := slices.IndexFunc(r.calls, func(c ghCall) bool { return c.is("issue", "create") })
	ensured := slices.IndexFunc(r.calls, func(c ghCall) bool {
		return c.is("label", "create", "fuzz-crasher") && slices.Contains(c.Args, "--force")
	})
	if ensured < 0 || ensured > firstCreate {
		t.Errorf("label fuzz-crasher not ensured with --force before the first issue create: %v", r.calls)
	}
}

func TestFuzzNightlyCommentsOnTheOpenIssueWithTheExactTitle(t *testing.T) {
	title := crasherTitle("internal/x", "FuzzCrash")
	issue := func(n int, title, state string, labels ...string) string {
		b, err := json.Marshal(map[string]any{
			"number": n, "title": title, "state": state, "labels": labels,
			"url": fmt.Sprintf("https://github.com/hallow-inc/example/issues/%d", n),
		})
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	issues := "[" + strings.Join([]string{
		issue(8, title, "closed", "fuzz-crasher"),
		issue(9, title, "open", "bug"),
		issue(5, title+"More", "open", "fuzz-crasher"),
		issue(6, strings.TrimSuffix(title, "h"), "open", "fuzz-crasher"),
		issue(4, crasherTitle("x", "FuzzCrash"), "open", "fuzz-crasher"),
		issue(7, title, "open", "fuzz-crasher"),
	}, ",") + "]"

	r := runFuzzNightly(t, map[string]string{
		"internal/x/x.go":      "package x\n",
		"internal/x/x_test.go": anyMutationFails,
	}, issues, "FUZZTIME=10s")

	if r.code == 0 {
		t.Errorf("a run with a crasher exited 0\n%s", r.out)
	}
	if creates := r.callsOf("issue", "create"); len(creates) != 0 {
		t.Errorf("an open fuzz-crasher issue has this title; opening another spams the tracker: %v", creates)
	}
	comments := r.callsOf("issue", "comment")
	if len(comments) != 1 || len(comments[0].Args) < 3 || comments[0].Args[2] != "7" {
		t.Fatalf("want one comment on #7, the only open fuzz-crasher issue titled %q; got %v\n%s", title, comments, r.out)
	}
	hash, contents := newInput(t, r, "internal/x", "FuzzCrash")
	if !fenced(comments[0].Body, contents) {
		t.Errorf("comment does not hold the input %q in a fence:\n%s", contents, comments[0].Body)
	}
	assertHandoff(t, comments[0].Body, "internal/x", "FuzzCrash", hash)
}

func TestFuzzNightlyNamesTheArtifactForAnOversizedInput(t *testing.T) {
	const runURL = "https://github.com/hallow-inc/claude-plugins/actions/runs/4242"
	// Failing needs the full length, so minimization cannot shrink the input below the limit.
	r := runFuzzNightly(t, map[string]string{
		"internal/big/big.go": "package big\n",
		"internal/big/big_test.go": "package big\n\nimport (\n\t\"bytes\"\n\t\"testing\"\n)\n\n" +
			"func FuzzHuge(f *testing.F) {\n\tseed := bytes.Repeat([]byte(\"a\"), 70000)\n\tf.Add(seed)\n" +
			"\tf.Fuzz(func(t *testing.T, b []byte) {\n\t\tif len(b) == len(seed) && !bytes.Equal(b, seed) {\n" +
			"\t\t\tt.Fatal(\"huge\")\n\t\t}\n\t})\n}\n",
	}, "[]", "FUZZTIME=5s", "RUN_URL="+runURL)

	hash, contents := newInput(t, r, "internal/big", "FuzzHuge")
	if len(contents) <= 60000 {
		t.Fatalf("precondition: planted input is %d bytes, not over 60000", len(contents))
	}
	if r.code == 0 {
		t.Errorf("a run with a crasher exited 0\n%s", r.out)
	}
	creates := r.callsOf("issue", "create")
	if len(creates) != 1 {
		t.Fatalf("want one issue, got %v\n%s", creates, r.out)
	}
	body := creates[0].Body
	if len(body) > 65536 {
		t.Errorf("body is %d characters; GitHub rejects bodies over 65536", len(body))
	}
	_, value, _ := strings.Cut(contents, "\n")
	if strings.Contains(body, value[:256]) {
		t.Errorf("oversized input pasted into the body")
	}
	for _, want := range []string{"too large", "fuzz-corpus", runURL} {
		if !strings.Contains(strings.ToLower(body), strings.ToLower(want)) {
			t.Errorf("body lacks %q, so bugfix cannot find the input:\n%s", want, body)
		}
	}
	assertHandoff(t, body, "internal/big", "FuzzHuge", hash)
}

func TestFuzzNightlyFailsWithoutFilingWhenNoInputIsWritten(t *testing.T) {
	r := runFuzzNightly(t, map[string]string{
		"internal/x/x.go": "package x\n",
		"internal/x/x_test.go": "package x\n\nimport \"testing\"\n\nfunc FuzzSeedFails(f *testing.F) {\n" +
			"\tf.Add(\"boom\")\n\tf.Fuzz(func(t *testing.T, s string) {\n\t\tif s == \"boom\" {\n" +
			"\t\t\tt.Fatal(\"seed\")\n\t\t}\n\t})\n}\n",
	}, "[]", "FUZZTIME=10s")

	if _, err := os.Stat(filepath.Join(r.mod, "internal", "x", "testdata")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("precondition: a failing seed should write no input, but testdata exists (%v)", err)
	}
	if r.code == 0 {
		t.Errorf("a failing target exited 0 because it wrote no input\n%s", r.out)
	}
	if filed := append(r.callsOf("issue", "create"), r.callsOf("issue", "comment")...); len(filed) != 0 {
		t.Errorf("no input to hand to bugfix, yet the script filed: %v", filed)
	}
}

func TestFuzzNightlyPassesQuietlyWhenNothingFails(t *testing.T) {
	r := runFuzzNightly(t, map[string]string{
		"internal/x/x.go": "package x\n",
		"internal/x/x_test.go": "package x\n\nimport \"testing\"\n\nfunc FuzzFine(f *testing.F) {\n" +
			"\tf.Add(\"ok\")\n\tf.Fuzz(func(t *testing.T, s string) {})\n}\n",
	}, "[]", "FUZZTIME=1s")

	if r.code != 0 {
		t.Errorf("a clean run exited %d\n%s", r.code, r.out)
	}
	if filed := append(r.callsOf("issue", "create"), r.callsOf("issue", "comment")...); len(filed) != 0 {
		t.Errorf("nothing failed, yet the script filed: %v", filed)
	}
}
