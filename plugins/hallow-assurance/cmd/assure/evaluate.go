package main

import (
	"cmp"
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
	ref := fs.String("changed-from", "", "git ref whose diff to the working tree, plus untracked files, is the changed set")
	mergeWith := fs.String("merge-base-with", "", "git ref whose merge base with HEAD is the changed-from point")
	report := fs.String("report", "", "report path (default <root>/"+app.ReportFile+")")
	date := fs.String("date", time.Now().UTC().Format(time.DateOnly), "evaluation date, YYYY-MM-DD, for waiver expiry")
	const use = "usage: assure evaluate (--changed-from <ref> | --merge-base-with <ref>) [--report <path>] [--date YYYY-MM-DD]"
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 || (*ref == "") == (*mergeWith == "") {
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
	from, err := changedFrom(m, *ref, *mergeWith)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "assure: %v\n", err)
		return 2
	}
	rep, err := app.Evaluate(m, from, *date)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "assure: %v\n", err)
		return 2
	}
	if err := writeReport(rep, cmp.Or(*report, filepath.Join(m.Root, app.ReportFile))); err != nil {
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

func changedFrom(m *core.Manifest, ref, mergeWith string) (string, error) {
	if mergeWith == "" {
		return ref, nil
	}
	return app.MergeBase(m.Root, mergeWith)
}

func writeReport(rep app.EvalReport, path string) error {
	data, err := rep.Marshal()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
