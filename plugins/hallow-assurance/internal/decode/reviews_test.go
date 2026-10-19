package decode_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"pgregory.net/rapid"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/decode"
	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/schemas"
)

var (
	shaGen   = rapid.StringMatching(`[0-9a-f]{40}`)
	loginGen = rapid.StringMatching(`[A-Za-z0-9][A-Za-z0-9-]{0,38}`)
	stateGen = rapid.SampledFrom([]string{"APPROVED", "CHANGES_REQUESTED", "COMMENTED", "DISMISSED", "PENDING"})
	extraGen = rapid.MapOfN(rapid.StringMatching(`x_[a-z]{1,6}`), rapid.OneOf(
		rapid.Just[any](nil), rapid.Map(rapid.Int(), func(i int) any { return i }), rapid.Map(rapid.String(), func(s string) any { return s }),
	), 0, 3)
)

func withExtras(t *rapid.T, label string, m map[string]any) map[string]any {
	for k, v := range extraGen.Draw(t, label) {
		m[k] = v
	}
	return m
}

func pullGen(t *rapid.T) (pull map[string]any, author, head string) {
	author, head = loginGen.Draw(t, "author"), shaGen.Draw(t, "head")
	pull = withExtras(t, "pullExtra", map[string]any{
		"number": rapid.IntRange(1, 99999).Draw(t, "number"),
		"state":  "open",
		"user":   withExtras(t, "userExtra", map[string]any{"login": author, "id": rapid.IntRange(1, 1<<30).Draw(t, "uid"), "type": "User"}),
		"head":   withExtras(t, "headExtra", map[string]any{"sha": head, "ref": "topic"}),
		"base":   map[string]any{"sha": shaGen.Draw(t, "base"), "ref": "master"},
	})
	return pull, author, head
}

func reviewGen(t *rapid.T) (review map[string]any, kept *decode.ReviewEntry) {
	login, state, commit := loginGen.Draw(t, "login"), stateGen.Draw(t, "state"), shaGen.Draw(t, "commit")
	review = withExtras(t, "reviewExtra", map[string]any{"id": rapid.IntRange(1, 1<<40).Draw(t, "id"), "state": state, "body": rapid.String().Draw(t, "body")})
	userOK, commitOK := true, true
	switch rapid.IntRange(0, 4).Draw(t, "user") {
	case 0:
		review["user"] = nil
		userOK = false
	case 1:
		userOK = false
	case 2:
		review["user"] = map[string]any{"id": 1, "login": nil}
		userOK = false
	case 3:
		review["user"] = map[string]any{"id": 1}
		userOK = false
	default:
		review["user"] = withExtras(t, "reviewerExtra", map[string]any{"login": login, "type": "User"})
	}
	switch rapid.IntRange(0, 3).Draw(t, "commit_id") {
	case 0:
		review["commit_id"] = nil
		commitOK = false
	case 1:
		commitOK = false
	default:
		review["commit_id"] = commit
	}
	if userOK && commitOK {
		kept = &decode.ReviewEntry{Login: login, State: state, Commit: commit}
	}
	return review, kept
}

func mustJSON(t interface{ Fatalf(string, ...any) }, v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func TestReviewsAPIKeepsEveryAttributableReviewInOrder(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		pull, author, head := pullGen(t)
		want := decode.ReviewsDoc{Author: author, Head: head, Reviews: []decode.ReviewEntry{}}
		pages := make([][]map[string]any, rapid.IntRange(0, 4).Draw(t, "pages"))
		for i := range pages {
			pages[i] = []map[string]any{}
			for range rapid.IntRange(0, 6).Draw(t, "perPage") {
				r, kept := reviewGen(t)
				pages[i] = append(pages[i], r)
				if kept != nil {
					want.Reviews = append(want.Reviews, *kept)
				}
			}
		}
		pullJSON, pagesJSON := mustJSON(t, pull), mustJSON(t, pages)
		got, err := decode.ReviewsAPI(pullJSON, pagesJSON)
		if err != nil {
			t.Fatalf("ReviewsAPI(%s, %s): %v", pullJSON, pagesJSON, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("pull %s pages %s\ngot  %+v\nwant %+v; a review with a user and a commit must survive, in page then API order, and no other", pullJSON, pagesJSON, got, want)
		}
		out := mustJSON(t, got)
		vs, err := schemas.Validate(schemas.Reviews, out)
		if err != nil || len(vs) > 0 {
			t.Fatalf("output %s fails reviews.schema.json: %v %v; evaluate --reviews would reject it", out, err, vs)
		}
	})
}

func TestReviewsAPIRejectsAPullWithoutAuthorOrHead(t *testing.T) {
	breaks := []struct {
		name  string
		apply func(p map[string]any) any
	}{
		{"no user", func(p map[string]any) any { delete(p, "user"); return p }},
		{"null user", func(p map[string]any) any { p["user"] = nil; return p }},
		{"no login", func(p map[string]any) any { delete(p["user"].(map[string]any), "login"); return p }},
		{"null login", func(p map[string]any) any { p["user"].(map[string]any)["login"] = nil; return p }},
		{"numeric login", func(p map[string]any) any { p["user"].(map[string]any)["login"] = 7; return p }},
		{"no head", func(p map[string]any) any { delete(p, "head"); return p }},
		{"null head", func(p map[string]any) any { p["head"] = nil; return p }},
		{"no sha", func(p map[string]any) any { delete(p["head"].(map[string]any), "sha"); return p }},
		{"null sha", func(p map[string]any) any { p["head"].(map[string]any)["sha"] = nil; return p }},
		{"numeric sha", func(p map[string]any) any { p["head"].(map[string]any)["sha"] = 40; return p }},
		{"array", func(p map[string]any) any { return []any{p} }},
		{"string", func(map[string]any) any { return "pull" }},
		{"number", func(map[string]any) any { return 39 }},
		{"null", func(map[string]any) any { return nil }},
	}
	rapid.Check(t, func(t *rapid.T) {
		pull, _, _ := pullGen(t)
		b := rapid.SampledFrom(breaks).Draw(t, "break")
		broken := mustJSON(t, b.apply(pull))
		if doc, err := decode.ReviewsAPI(broken, []byte(`[]`)); err == nil {
			t.Fatalf("%s: accepted %s as %+v; a reviews file without the author or head cannot decide independence", b.name, broken, doc)
		}
	})
}

func TestReviewsAPIRejectsPagesThatAreNotArraysOfArrays(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		pull, _, _ := pullGen(t)
		review, _ := reviewGen(t)
		pages := rapid.SampledFrom([]any{
			[]any{review},
			map[string]any{"reviews": []any{review}},
			[]any{[]any{review}, review},
			[]any{nil},
			[]any{"page"},
			"pages",
			3,
			nil,
		}).Draw(t, "pages")
		pagesJSON := mustJSON(t, pages)
		if doc, err := decode.ReviewsAPI(mustJSON(t, pull), pagesJSON); err == nil {
			t.Fatalf("accepted pages %s as %+v; without --slurp, gh concatenates pages and later pages would be lost silently", pagesJSON, doc)
		}
	})
}

func TestReviewsAPIDecodesRecordedResponse(t *testing.T) {
	pull, err := os.ReadFile(filepath.Join("testdata", "reviews-api", "pull.json"))
	if err != nil {
		t.Fatal(err)
	}
	pages, err := os.ReadFile(filepath.Join("testdata", "reviews-api", "pages.json"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := decode.ReviewsAPI(pull, pages)
	if err != nil {
		t.Fatal(err)
	}
	want := decode.ReviewsDoc{
		Author: "hello-world-bfree",
		Head:   "2029641a7c3e5b9d0f1e2d3c4b5a69788796a5b4",
		Reviews: []decode.ReviewEntry{
			{Login: "octo-reviewer", State: "COMMENTED", Commit: "0f1e2d3c4b5a69788796a5b4c3d2e1f00f1e2d3c"},
			{Login: "second-reviewer", State: "APPROVED", Commit: "2029641a7c3e5b9d0f1e2d3c4b5a69788796a5b4"},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

func TestReviewsAPIEmptyReviewsMarshalAsArray(t *testing.T) {
	got, err := decode.ReviewsAPI([]byte(`{"user":{"login":"a"},"head":{"sha":"0123456789abcdef0123456789abcdef01234567"}}`), []byte(`[]`))
	if err != nil {
		t.Fatal(err)
	}
	if out := string(mustJSON(t, got)); out != `{"author":"a","head":"0123456789abcdef0123456789abcdef01234567","reviews":[]}` {
		t.Fatalf("got %s; reviews must be [] so the file validates", out)
	}
}
