package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/decode"
	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/schemas"
)

const (
	reviewsUse  = "usage: assure reviews --pr <n> [--repo <owner/name>]"
	ghTimeout   = 2 * time.Minute
	ghMaxStderr = 64 << 10
)

var ghMaxStdout = 64 << 20

var errGhOverflow = errors.New("gh output exceeds the size cap")

type ghCapped struct {
	buf      bytes.Buffer
	max      int
	overflow bool
}

func (c *ghCapped) Write(p []byte) (int, error) {
	if room := c.max - c.buf.Len(); len(p) > room {
		n, _ := c.buf.Write(p[:max(room, 0)])
		c.overflow = true
		return n, errGhOverflow
	}
	return c.buf.Write(p)
}

type ghFailure struct {
	stderr string
	err    error
}

func (e *ghFailure) Error() string {
	if e.stderr == "" {
		return e.err.Error()
	}
	return e.err.Error() + ": " + e.stderr
}

func runReviews(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("reviews", flag.ContinueOnError)
	fs.SetOutput(stderr)
	prFlag := fs.String("pr", "", "pull request number")
	repo := fs.String("repo", "", "owner/name (default: the repository gh resolves for the current directory)")
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 {
		_, _ = fmt.Fprintln(stderr, reviewsUse)
		return 2
	}
	n, err := strconv.Atoi(*prFlag)
	if err != nil || n <= 0 {
		_, _ = fmt.Fprintln(stderr, reviewsUse)
		return 2
	}
	gh, err := exec.LookPath("gh")
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "assure reviews: gh is not on PATH; install gh (https://cli.github.com) and authenticate with gh auth login or GH_TOKEN")
		return 2
	}
	pr := strconv.Itoa(n)
	where := "the repository gh resolves for this directory"
	if *repo != "" {
		where = *repo
	}
	pull, err := ghAPI(gh, *repo, "repos/{owner}/{repo}/pulls/"+pr)
	if err == nil {
		var pages []byte
		pages, err = ghAPI(gh, *repo, "--paginate", "--slurp", "repos/{owner}/{repo}/pulls/"+pr+"/reviews")
		if err == nil {
			return printReviews(pull, pages, pr, where, stdout, stderr)
		}
	}
	_, _ = fmt.Fprintln(stderr, ghAdvice(err, pr, where))
	return 2
}

func printReviews(pull, pages []byte, pr, where string, stdout, stderr io.Writer) int {
	doc, err := decode.ReviewsAPI(pull, pages)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "assure reviews: unexpected GitHub API response for pull request %s in %s: %v\n", pr, where, err)
		return 2
	}
	data, err := json.Marshal(doc)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "assure reviews: %v\n", err)
		return 2
	}
	data = append(data, '\n')
	vs, err := schemas.Validate(schemas.Reviews, data)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "assure reviews: %v\n", err)
		return 2
	}
	if len(vs) > 0 {
		_, _ = fmt.Fprintf(stderr, "assure reviews: the reviews of pull request %s in %s do not fit schemas/reviews.schema.json:\n", pr, where)
		for _, v := range vs {
			_, _ = fmt.Fprintf(stderr, "  %s\n", v)
		}
		return 2
	}
	if _, err := stdout.Write(data); err != nil {
		_, _ = fmt.Fprintf(stderr, "assure reviews: %v\n", err)
		return 1
	}
	return 0
}

func ghAPI(gh, repo string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), ghTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, gh, append([]string{"api"}, args...)...)
	cmd.WaitDelay = time.Second
	if repo != "" {
		cmd.Env = append(os.Environ(), "GH_REPO="+repo)
	}
	out := &ghCapped{max: ghMaxStdout}
	errOut := &ghCapped{max: ghMaxStderr}
	cmd.Stdout, cmd.Stderr = out, errOut
	err := cmd.Run()
	if out.overflow {
		return nil, &ghFailure{err: errGhOverflow}
	}
	if err != nil {
		return nil, &ghFailure{stderr: strings.TrimSpace(errOut.buf.String()), err: err}
	}
	return out.buf.Bytes(), nil
}

func ghAdvice(err error, pr, where string) string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "gh auth login") || strings.Contains(msg, "GH_TOKEN") || strings.Contains(msg, "HTTP 401"):
		return "assure reviews: gh is not authenticated; run gh auth login or set GH_TOKEN"
	case strings.Contains(msg, "HTTP 404"):
		return fmt.Sprintf("assure reviews: pull request %s was not found in %s; check the pull request number and repository", pr, where)
	default:
		return fmt.Sprintf("assure reviews: could not read pull request %s in %s (%v); check the pull request number and repository", pr, where, err)
	}
}
