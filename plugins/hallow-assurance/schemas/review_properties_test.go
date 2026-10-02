package schemas

import "pgregory.net/rapid"

var (
	loginGen  = rapid.StringMatching(`[A-Za-z][A-Za-z0-9-]{0,10}`)
	stateGen  = rapid.SampledFrom([]string{"APPROVED", "CHANGES_REQUESTED", "COMMENTED", "DISMISSED", "PENDING"})
	commitGen = blobGen
)

var reviewsGen = rapid.Custom(func(t *rapid.T) sample {
	r := &rec{}
	doc := map[string]any{
		"author": loginGen.Draw(t, "author"),
		"head":   commitGen.Draw(t, "head"),
	}
	r.root("x", 5, []any{})
	r.closed("")
	r.required("", "author", "head", "reviews")
	r.bad("", "author", badText...)
	r.bad("", "head", badSHA...)
	r.bad("", "reviews", "x", map[string]any{}, []any{"x"})
	reviews := []any{}
	for i := range rapid.IntRange(0, 4).Draw(t, "n") {
		p := "/reviews/" + idx(i)
		reviews = append(reviews, map[string]any{
			"login":  loginGen.Draw(t, "login"),
			"state":  stateGen.Draw(t, "state"),
			"commit": commitGen.Draw(t, "commit"),
		})
		r.bad("/reviews", idx(i), "x", 7)
		r.closed(p)
		r.required(p, "login", "state", "commit")
		r.bad(p, "login", badText...)
		r.bad(p, "state", "LGTM", "approved", 1)
		r.bad(p, "commit", badSHA...)
	}
	doc["reviews"] = reviews
	return sample{doc, r.cs}
})

var pendingGen = rapid.Custom(func(t *rapid.T) sample {
	r := &rec{}
	doc := map[string]any{"v": 0, "path": pathGen.Draw(t, "path"), "pre": nil}
	if rapid.Bool().Draw(t, "exists") {
		doc["pre"] = blobGen.Draw(t, "pre")
	}
	r.root("x", 5, []any{})
	r.closed("")
	r.required("", "v", "path", "pre")
	r.bad("", "v", 1, "0")
	r.bad("", "path", badPath...)
	r.bad("", "pre", badBlob...)
	return sample{doc, r.cs}
})
