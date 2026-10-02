package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/app"
	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/core"
)

func runEvaluate(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("evaluate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	ref := fs.String("changed-from", "", "git ref whose diff to the working tree, plus untracked files, is the changed set (required)")
	report := fs.String("report", "", "report path (default <root>/"+app.ReportFile+")")
	date := fs.String("date", time.Now().UTC().Format(time.DateOnly), "evaluation date, YYYY-MM-DD, for waiver expiry")
	reviewsFile := fs.String("reviews", "", "JSON file of the PR author, head commit, and reviews; a non-author approval on head excuses gaps and protected-file changes")
	const use = "usage: assure evaluate --changed-from <ref> [--report <path>] [--date YYYY-MM-DD] [--reviews <file>]"
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 || *ref == "" {
		_, _ = fmt.Fprintln(stderr, use)
		return 2
	}
	if _, err := time.Parse(time.DateOnly, *date); err != nil {
		_, _ = fmt.Fprintf(stderr, "assure: --date %q is not YYYY-MM-DD\n", *date)
		return 2
	}
	m := cwdManifest(stderr)
	if m == nil {
		return 2
	}
	var reviews *core.Reviews
	if *reviewsFile != "" {
		r, err := app.LoadReviews(m, *reviewsFile)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "assure: %v\n", err)
			return 2
		}
		reviews = r
	}
	rep, err := app.EvaluateWith(m, *ref, *date, reviews)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "assure: %v\n", err)
		return 2
	}
	data, err := rep.Marshal()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "assure: %v\n", err)
		return 2
	}
	path := *report
	if path == "" {
		path = filepath.Join(m.Root, app.ReportFile)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		_, _ = fmt.Fprintf(stderr, "assure: %v\n", err)
		return 2
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		_, _ = fmt.Fprintf(stderr, "assure: %v\n", err)
		return 2
	}
	if _, err := io.WriteString(stdout, rep.Summary()); err != nil {
		_, _ = fmt.Fprintf(stderr, "assure: %v\n", err)
		return 1
	}
	if rep.Blocking() {
		return 1
	}
	return 0
}
