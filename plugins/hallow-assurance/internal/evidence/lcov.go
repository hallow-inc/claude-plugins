package evidence

import (
	"bufio"
	"bytes"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

type LCOVFile struct {
	Path      string
	Functions []LCOVFunction
	Lines     map[int]int
}

type LCOVFunction struct {
	Name  string
	Start int
	End   int
	Hits  int
}

type lcovBlock struct {
	file  LCOVFile
	funcs map[string]int
}

func ParseLCOV(data []byte) ([]LCOVFile, error) {
	var out []LCOVFile
	var cur *lcovBlock
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	n := 0
	for sc.Scan() {
		n++
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		key, val, _ := strings.Cut(line, ":")
		if key == "TN" {
			continue
		}
		if key == "SF" {
			if cur != nil {
				return nil, lcovErr(n, "SF inside an unterminated record")
			}
			if !repoRelative(val) {
				return nil, lcovErr(n, "SF %q is not a repo-relative path", val)
			}
			cur = &lcovBlock{file: LCOVFile{Path: val, Lines: map[int]int{}}, funcs: map[string]int{}}
			continue
		}
		if cur == nil {
			return nil, lcovErr(n, "%s record outside an SF block", key)
		}
		if key == "end_of_record" {
			out = append(out, cur.file)
			cur = nil
			continue
		}
		if err := cur.record(key, val); err != nil {
			return nil, lcovErr(n, "%v", err)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("lcov: %w", err)
	}
	if cur != nil {
		return nil, fmt.Errorf("lcov: %s has no end_of_record", cur.file.Path)
	}
	return out, nil
}

func lcovErr(line int, format string, args ...any) error {
	return fmt.Errorf("lcov: line %d: %s", line, fmt.Sprintf(format, args...))
}

func (b *lcovBlock) record(key, val string) error {
	switch key {
	case "FN":
		f, err := parseFN(val)
		if err != nil {
			return err
		}
		b.funcs[f.Name] = len(b.file.Functions)
		b.file.Functions = append(b.file.Functions, f)
	case "FNDA":
		hits, name, ok := strings.Cut(val, ",")
		h, err := strconv.Atoi(hits)
		i, known := b.funcs[name]
		if !ok || err != nil || h < 0 || !known {
			return fmt.Errorf("malformed FNDA %q", val)
		}
		b.file.Functions[i].Hits = h
	case "DA":
		l, h, err := parseDA(val)
		if err != nil {
			return err
		}
		b.file.Lines[l] = h
	}
	return nil
}

func parseFN(val string) (LCOVFunction, error) {
	parts := strings.Split(val, ",")
	bad := fmt.Errorf("malformed FN %q", val)
	if len(parts) < 2 || len(parts) > 3 {
		return LCOVFunction{}, bad
	}
	f := LCOVFunction{Name: parts[len(parts)-1]}
	var err error
	if f.Start, err = lineNumber(parts[0]); err != nil {
		return LCOVFunction{}, bad
	}
	if len(parts) == 3 {
		if f.End, err = lineNumber(parts[1]); err != nil || f.End < f.Start {
			return LCOVFunction{}, bad
		}
	}
	if f.Name == "" {
		return LCOVFunction{}, bad
	}
	return f, nil
}

func parseDA(val string) (int, int, error) {
	parts := strings.Split(val, ",")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, 0, fmt.Errorf("malformed DA %q", val)
	}
	l, err := lineNumber(parts[0])
	h, herr := strconv.Atoi(parts[1])
	if err != nil || herr != nil || h < 0 {
		return 0, 0, fmt.Errorf("malformed DA %q", val)
	}
	return l, h, nil
}

func lineNumber(s string) (int, error) {
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 {
		return 0, fmt.Errorf("bad line number %q", s)
	}
	return n, nil
}

func FormatLCOV(files []LCOVFile) []byte {
	var b bytes.Buffer
	for _, f := range files {
		fmt.Fprintf(&b, "SF:%s\n", f.Path)
		for _, fn := range f.Functions {
			if fn.End != 0 {
				fmt.Fprintf(&b, "FN:%d,%d,%s\n", fn.Start, fn.End, fn.Name)
			} else {
				fmt.Fprintf(&b, "FN:%d,%s\n", fn.Start, fn.Name)
			}
		}
		for _, fn := range f.Functions {
			fmt.Fprintf(&b, "FNDA:%d,%s\n", fn.Hits, fn.Name)
		}
		lines := make([]int, 0, len(f.Lines))
		for l := range f.Lines {
			lines = append(lines, l)
		}
		sort.Ints(lines)
		for _, l := range lines {
			fmt.Fprintf(&b, "DA:%d,%d\n", l, f.Lines[l])
		}
		b.WriteString("end_of_record\n")
	}
	return b.Bytes()
}
