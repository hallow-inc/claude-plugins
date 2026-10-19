package decode

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
)

type ReviewsDoc struct {
	Author  string        `json:"author"`
	Head    string        `json:"head"`
	Reviews []ReviewEntry `json:"reviews"`
}

type ReviewEntry struct {
	Login  string `json:"login"`
	State  string `json:"state"`
	Commit string `json:"commit"`
}

type apiUser struct {
	Login *string `json:"login"`
}

type apiPull struct {
	User *apiUser `json:"user"`
	Head *struct {
		SHA *string `json:"sha"`
	} `json:"head"`
}

type apiReview struct {
	User     *apiUser `json:"user"`
	State    string   `json:"state"`
	CommitID *string  `json:"commit_id"`
}

func ReviewsAPI(pull, pages []byte) (ReviewsDoc, error) {
	author, head, err := pullAuthorHead(pull)
	if err != nil {
		return ReviewsDoc{}, err
	}
	var ps []*[]*apiReview
	if err := json.Unmarshal(pages, &ps); err != nil {
		return ReviewsDoc{}, fmt.Errorf("reviews: %w", err)
	}
	if ps == nil || slices.Contains(ps, nil) {
		return ReviewsDoc{}, errors.New("reviews: not an array of pages")
	}
	doc := ReviewsDoc{Author: author, Head: head, Reviews: []ReviewEntry{}}
	for _, page := range ps {
		for _, r := range *page {
			if e, ok := r.entry(); ok {
				doc.Reviews = append(doc.Reviews, e)
			}
		}
	}
	return doc, nil
}

func pullAuthorHead(pull []byte) (author, head string, err error) {
	var p *apiPull
	if err := json.Unmarshal(pull, &p); err != nil {
		return "", "", fmt.Errorf("pull request: %w", err)
	}
	if p == nil || p.User == nil || p.User.Login == nil {
		return "", "", errors.New("pull request: no user.login")
	}
	if p.Head == nil || p.Head.SHA == nil {
		return "", "", errors.New("pull request: no head.sha")
	}
	return *p.User.Login, *p.Head.SHA, nil
}

func (r *apiReview) entry() (ReviewEntry, bool) {
	if r == nil || r.User == nil || r.User.Login == nil || r.CommitID == nil {
		return ReviewEntry{}, false
	}
	return ReviewEntry{Login: *r.User.Login, State: r.State, Commit: *r.CommitID}, true
}
