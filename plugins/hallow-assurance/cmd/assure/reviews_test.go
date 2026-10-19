package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"pgregory.net/rapid"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/schemas"
)

type ghReview struct {
	User     any    `json:"user"`
	State    string `json:"state"`
	CommitID any    `json:"commit_id"`
	ID       int    `json:"id"`
	Body     string `json:"body"`
}

type ghFake struct {
	t   *testing.T
	dir string
	log string
}

func reviewer(login string) any { return map[string]any{"login": login, "id": 1, "type": "User"} }

func pullBody(author, head string) string {
	return fmt.Sprintf(`{"number":7,"state":"open","user":{"login":%q,"id":9,"type":"User"},"head":{"ref":"topic","sha":%q},"base":{"ref":"master","sha":"%s"}}`, author, head, strings.Repeat("b", 40))
}

// newGH writes a fake gh that answers the pull request endpoint with pull and the reviews endpoint
// with pages: slurped into one array of arrays only when called with --paginate and --slurp, and
// only the first page otherwise, as gh does.
func newGH(t *testing.T, pr int, pull string, pages [][]ghReview) ghFake {
	t.Helper()
	g := ghFake{t: t, dir: t.TempDir()}
	g.log = filepath.Join(g.dir, "calls.log")
	first := []ghReview{}
	if len(pages) > 0 {
		first = pages[0]
	}
	g.file("pull.json", pull)
	g.file("slurped.json", string(mustMarshal(t, pages)))
	g.file("first.json", string(mustMarshal(t, first)))
	n := strconv.Itoa(pr)
	g.script(`case " $* " in
*" --paginate "*)
  case " $* " in *" --slurp "*) slurp=1 ;; esac ;;
esac
case "$*" in
*"/pulls/` + n + `/reviews"*)
  if [ -n "$slurp" ]; then /bin/cat '` + g.dir + `/slurped.json'; else /bin/cat '` + g.dir + `/first.json'; fi
  exit 0 ;;
*"/pulls/` + n + `"*) /bin/cat '` + g.dir + `/pull.json'; exit 0 ;;
esac
printf 'fake gh: unexpected call %s\n' "$*" >&2
exit 1`)
	return g
}

func failingGH(t *testing.T, stderr string, exit int) ghFake {
	t.Helper()
	g := ghFake{t: t, dir: t.TempDir()}
	g.log = filepath.Join(g.dir, "calls.log")
	g.file("stderr.txt", stderr)
	g.script(fmt.Sprintf("/bin/cat '%s/stderr.txt' >&2\nexit %d", g.dir, exit))
	return g
}

func (g ghFake) file(name, body string) {
	g.t.Helper()
	if err := os.WriteFile(filepath.Join(g.dir, name), []byte(body), 0o644); err != nil {
		g.t.Fatal(err)
	}
}

func (g ghFake) script(body string) {
	g.t.Helper()
	sh := "#!/bin/sh\nprintf '%s GH_REPO=%s\\n' \"$*\" \"$GH_REPO\" >> '" + g.log + "'\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(g.dir, "gh"), []byte(sh), 0o755); err != nil {
		g.t.Fatal(err)
	}
}

func (g ghFake) calls() string {
	g.t.Helper()
	b, err := os.ReadFile(g.log)
	if errors.Is(err, os.ErrNotExist) {
		return ""
	}
	if err != nil {
		g.t.Fatal(err)
	}
	return string(b)
}

func mustMarshal(t interface{ Fatalf(string, ...any) }, v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

type reviewsOut struct {
	Author  string `json:"author"`
	Head    string `json:"head"`
	Reviews []struct {
		Login  string `json:"login"`
		State  string `json:"state"`
		Commit string `json:"commit"`
	} `json:"reviews"`
}

func parseReviewsOut(t *testing.T, stdout string) reviewsOut {
	t.Helper()
	if !strings.HasSuffix(stdout, "\n") {
		t.Fatalf("stdout %q does not end in a newline", stdout)
	}
	if vs, err := schemas.Validate(schemas.Reviews, []byte(stdout)); err != nil || len(vs) > 0 {
		t.Fatalf("stdout fails reviews.schema.json: %v %v\n%s", err, vs, stdout)
	}
	dec := json.NewDecoder(strings.NewReader(stdout))
	var out reviewsOut
	if err := dec.Decode(&out); err != nil {
		t.Fatal(err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		t.Fatalf("stdout holds more than one JSON document: %q", stdout)
	}
	return out
}

func onlyOnPath(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("PATH", dir)
}

func emptyWorkdir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	return dir
}

func TestReviewsListsEveryReviewAcrossPagesInOrder(t *testing.T) {
	head, older := strings.Repeat("1", 40), strings.Repeat("2", 40)
	states := []string{"APPROVED", "CHANGES_REQUESTED", "COMMENTED", "DISMISSED", "PENDING"}
	var all []ghReview
	all = append(all, ghReview{User: reviewer("bob"), State: "APPROVED", CommitID: head, ID: 1},
		ghReview{User: reviewer("carol"), State: "COMMENTED", CommitID: older, ID: 2})
	for i := 3; i <= 150; i++ {
		commit := head
		if i%3 == 0 {
			commit = older
		}
		all = append(all, ghReview{User: reviewer(fmt.Sprintf("r%03d", i)), State: states[i%len(states)], CommitID: commit, ID: i})
	}
	g := newGH(t, 7, pullBody("alice", head), [][]ghReview{all[:100], all[100:]})
	onlyOnPath(t, g.dir)
	wd := emptyWorkdir(t)
	code, stdout, stderr := assure("reviews", "--pr", "7")
	if code != 0 {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	out := parseReviewsOut(t, stdout)
	if out.Author != "alice" || out.Head != head {
		t.Fatalf("author %q head %q; want the pull request's author and head commit", out.Author, out.Head)
	}
	if len(out.Reviews) != len(all) {
		t.Fatalf("%d reviews, want %d; a review on the second page decides independence as much as one on the first", len(out.Reviews), len(all))
	}
	for i, r := range out.Reviews {
		w := all[i]
		if r.Login != w.User.(map[string]any)["login"] || r.State != w.State || r.Commit != w.CommitID {
			t.Fatalf("review %d is %+v, want %+v in API order", i, r, w)
		}
	}
	if entries, err := os.ReadDir(wd); err != nil || len(entries) != 0 {
		t.Fatalf("reviews wrote into the working directory: %v %v", entries, err)
	}
}

func TestReviewsOmitsReviewWithoutUserOrCommit(t *testing.T) {
	head := strings.Repeat("3", 40)
	page := []ghReview{
		{User: reviewer("bob"), State: "APPROVED", CommitID: head, ID: 1},
		{User: nil, State: "APPROVED", CommitID: head, ID: 2},
		{User: reviewer("dave"), State: "COMMENTED", CommitID: nil, ID: 3},
		{User: reviewer("erin"), State: "CHANGES_REQUESTED", CommitID: head, ID: 4},
	}
	g := newGH(t, 7, pullBody("alice", head), [][]ghReview{page})
	onlyOnPath(t, g.dir)
	emptyWorkdir(t)
	code, stdout, stderr := assure("reviews", "--pr", "7")
	if code != 0 {
		t.Fatalf("exit %d, stderr %q; a deleted reviewer must not make the reviews file unobtainable", code, stderr)
	}
	out := parseReviewsOut(t, stdout)
	var logins []string
	for _, r := range out.Reviews {
		logins = append(logins, r.Login)
	}
	if !reflect.DeepEqual(logins, []string{"bob", "erin"}) {
		t.Fatalf("reviews by %v, want [bob erin]", logins)
	}
}

func TestReviewsWithoutGhOnPathSaysToInstallIt(t *testing.T) {
	onlyOnPath(t, t.TempDir())
	emptyWorkdir(t)
	code, stdout, stderr := assure("reviews", "--pr", "1")
	if code != 2 || stdout != "" || !strings.Contains(strings.ToLower(stderr), "install gh") {
		t.Fatalf("got %d stdout %q stderr %q; want exit 2, empty stdout, and advice to install gh", code, stdout, stderr)
	}
}

func TestReviewsUnauthenticatedNamesBothFixes(t *testing.T) {
	g := failingGH(t, "To get started with GitHub CLI, please run:  gh auth login\nAlternatively, populate the GH_TOKEN environment variable with a GitHub API authentication token.\n", 4)
	onlyOnPath(t, g.dir)
	emptyWorkdir(t)
	code, stdout, stderr := assure("reviews", "--pr", "1")
	if code != 2 || stdout != "" || !strings.Contains(stderr, "gh auth login") || !strings.Contains(stderr, "GH_TOKEN") {
		t.Fatalf("got %d stdout %q stderr %q; want exit 2, empty stdout, and both gh auth login and GH_TOKEN named", code, stdout, stderr)
	}
}

func TestReviewsUnknownPullNamesNumberAndRepository(t *testing.T) {
	g := failingGH(t, "gh: Not Found (HTTP 404)\n", 1)
	onlyOnPath(t, g.dir)
	emptyWorkdir(t)
	code, stdout, stderr := assure("reviews", "--repo", "acme/widgets", "--pr", "999999")
	if code != 2 || stdout != "" || !strings.Contains(stderr, "999999") || !strings.Contains(stderr, "acme/widgets") {
		t.Fatalf("got %d stdout %q stderr %q; want exit 2, empty stdout, and the number and repository to check", code, stdout, stderr)
	}
	if calls := g.calls(); !strings.Contains(calls, "GH_REPO=acme/widgets") {
		t.Fatalf("gh calls %q; --repo must reach gh as GH_REPO, since gh api takes no --repo flag", calls)
	}
}

func TestReviewsBadCredentialsAreClassifiedAsAuth(t *testing.T) {
	g := failingGH(t, "gh: Bad credentials (HTTP 401)\n", 1)
	onlyOnPath(t, g.dir)
	emptyWorkdir(t)
	code, stdout, stderr := assure("reviews", "--pr", "1")
	if code != 2 || stdout != "" || !strings.Contains(stderr, "gh auth login") || !strings.Contains(stderr, "GH_TOKEN") {
		t.Fatalf("got %d stdout %q stderr %q; gh's 401 text names neither fix, so assure must classify it and name gh auth login and GH_TOKEN", code, stdout, stderr)
	}
}

func TestReviewsNotFoundAndGenericFailuresAreDistinguished(t *testing.T) {
	g := failingGH(t, "gh: Not Found (HTTP 404)\n", 1)
	onlyOnPath(t, g.dir)
	emptyWorkdir(t)
	if code, stdout, stderr := assure("reviews", "--repo", "acme/widgets", "--pr", "4242"); code != 2 || stdout != "" || !strings.Contains(stderr, "was not found") {
		t.Fatalf("404: got %d stdout %q stderr %q; want the not-found advice", code, stdout, stderr)
	}
	g = failingGH(t, "gh: Something broke (HTTP 500)\n", 1)
	onlyOnPath(t, g.dir)
	code, stdout, stderr := assure("reviews", "--repo", "acme/widgets", "--pr", "4242")
	if code != 2 || stdout != "" || !strings.Contains(stderr, "gh: Something broke (HTTP 500)") || !strings.Contains(stderr, "4242") || !strings.Contains(stderr, "acme/widgets") {
		t.Fatalf("500: got %d stdout %q stderr %q; want exit 2 with gh's own text, the number, and the repository", code, stdout, stderr)
	}
	if strings.Contains(stderr, "was not found") || strings.Contains(stderr, "not authenticated") {
		t.Fatalf("500 misclassified: %q; a server error is neither a missing pull request nor a login problem", stderr)
	}
}

func setStdoutCap(t *testing.T, n int) {
	t.Helper()
	saved := ghMaxStdout
	t.Cleanup(func() { ghMaxStdout = saved })
	ghMaxStdout = n
}

func TestReviewsGhOutputCap(t *testing.T) {
	head := strings.Repeat("6", 40)
	pull := pullBody("alice", head)
	g := newGH(t, 7, pull, [][]ghReview{})
	onlyOnPath(t, g.dir)
	emptyWorkdir(t)

	setStdoutCap(t, len(pull))
	if code, stdout, stderr := assure("reviews", "--pr", "7"); code != 0 || parseReviewsOut(t, stdout).Head != head {
		t.Fatalf("output of exactly the cap (%d bytes): got %d %q %q; the cap is a ceiling, not a strict bound", len(pull), code, stdout, stderr)
	}

	setStdoutCap(t, len(pull)-1)
	code, stdout, stderr := assure("reviews", "--pr", "7")
	if code != 2 || stdout != "" || !strings.Contains(stderr, "size cap") {
		t.Fatalf("output one byte over the cap: got %d stdout %q stderr %q; want exit 2 naming the size cap", code, stdout, stderr)
	}
}

func TestReviewsHeadThatFailsTheSchemaIsNotPrinted(t *testing.T) {
	g := newGH(t, 7, pullBody("alice", "abc"), [][]ghReview{})
	onlyOnPath(t, g.dir)
	emptyWorkdir(t)
	code, stdout, stderr := assure("reviews", "--pr", "7")
	if code != 2 || stdout != "" || !strings.Contains(stderr, "schemas/reviews.schema.json") {
		t.Fatalf("got %d stdout %q stderr %q; a document evaluate would reject must fail here, before it is written", code, stdout, stderr)
	}
}

func TestReviewsUndecodablePullResponse(t *testing.T) {
	g := newGH(t, 7, `[]`, [][]ghReview{})
	onlyOnPath(t, g.dir)
	emptyWorkdir(t)
	code, stdout, stderr := assure("reviews", "--pr", "7")
	if code != 2 || stdout != "" || !strings.Contains(stderr, "unexpected GitHub API response") {
		t.Fatalf("got %d stdout %q stderr %q", code, stdout, stderr)
	}
}

func TestReviewsRepoFlagReachesEveryGhCall(t *testing.T) {
	head := strings.Repeat("4", 40)
	g := newGH(t, 12, pullBody("alice", head), [][]ghReview{{{User: reviewer("bob"), State: "APPROVED", CommitID: head, ID: 1}}})
	onlyOnPath(t, g.dir)
	emptyWorkdir(t)
	if code, _, stderr := assure("reviews", "--pr", "12", "--repo", "acme/widgets"); code != 0 {
		t.Fatalf("exit %d %q", code, stderr)
	}
	lines := strings.Split(strings.TrimSpace(g.calls()), "\n")
	if len(lines) != 2 {
		t.Fatalf("gh calls %q; want one for the pull request and one for its reviews", lines)
	}
	for _, l := range lines {
		if !strings.HasSuffix(l, " GH_REPO=acme/widgets") {
			t.Fatalf("gh call %q ran without GH_REPO=acme/widgets; it would read the checkout's repository instead", l)
		}
	}
}

func TestReviewsBadPullNumberPrintsUsageWithoutRunningGh(t *testing.T) {
	g := newGH(t, 1, pullBody("alice", strings.Repeat("5", 40)), nil)
	onlyOnPath(t, g.dir)
	emptyWorkdir(t)
	check := func(t interface{ Fatalf(string, ...any) }, args []string) {
		var out, errb bytes.Buffer
		code := run(args, &out, &errb)
		if code != 2 || out.Len() != 0 || !strings.Contains(strings.ToLower(errb.String()), "usage") || !strings.Contains(errb.String(), "--pr") {
			t.Fatalf("%q: got %d stdout %q stderr %q; want exit 2, empty stdout, and usage naming --pr", args, code, out.String(), errb.String())
		}
		if calls := g.calls(); calls != "" {
			t.Fatalf("%q ran gh: %q", args, calls)
		}
	}
	check(t, []string{"reviews"})
	check(t, []string{"reviews", "--repo", "acme/widgets"})
	for _, v := range []string{"abc", "0", "-1"} {
		check(t, []string{"reviews", "--pr", v})
	}
	rapid.Check(t, func(rt *rapid.T) {
		v := rapid.OneOf(
			rapid.StringMatching(`-[0-9]{1,6}`),
			rapid.StringMatching(`[a-zA-Z][a-zA-Z0-9]{0,6}`),
			rapid.StringMatching(`[1-9][0-9]{0,3}\.[0-9]{1,3}`),
			rapid.Just("0"),
			rapid.Just(""),
		).Draw(rt, "pr")
		check(rt, []string{"reviews", "--pr", v})
	})
}

func TestReviewsOutputLoadsInEvaluate(t *testing.T) {
	f := provBase(t, xxManifest, nil)
	handEdit(f)
	f.commit()
	head := f.head()
	g := newGH(t, 5, pullBody("owner", head), [][]ghReview{{{User: reviewer("reviewer"), State: "APPROVED", CommitID: head, ID: 1}}})
	t.Setenv("PATH", g.dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	code, stdout, stderr := assure("reviews", "--pr", "5")
	if code != 0 {
		t.Fatalf("reviews: exit %d %q", code, stderr)
	}
	file := filepath.Join(t.TempDir(), "r.json")
	if err := os.WriteFile(file, []byte(stdout), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _, stderr, _ := f.evaluate("--reviews", file); code == 2 {
		t.Fatalf("evaluate rejected the reviews output: %s", stderr)
	}
}
