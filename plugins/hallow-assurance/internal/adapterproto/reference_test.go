package adapterproto

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

func TestReferenceReturnsBoundedNonEmptyOutputOrNamesTheAdapter(t *testing.T) {
	state := t.TempDir()
	out, code := filepath.Join(state, "out"), filepath.Join(state, "code")
	fakeAdapter(t, "go", `[ "$1" = reference ] || exit 99
cat '`+out+`'
exit "$(cat '`+code+`')"`)
	dir := t.TempDir()
	rapid.Check(t, func(t *rapid.T) {
		unit := rapid.SampledFrom([]string{"# Go testing\n", "a", " \n\t", "\n"}).Draw(t, "unit")
		size := rapid.OneOf(
			rapid.IntRange(0, 64),
			rapid.IntRange(MaxReference-2, MaxReference+2),
			rapid.IntRange(0, 300<<10),
		).Draw(t, "size")
		body := []byte(strings.Repeat(unit, size/len(unit)+1)[:size])
		exit := rapid.SampledFrom([]int{0, 0, 1, 3}).Draw(t, "exit")
		if err := os.WriteFile(out, body, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(code, []byte(strconv.Itoa(exit)), 0o644); err != nil {
			t.Fatal(err)
		}
		got, err := Reference(dir, "go")
		ok := exit == 0 && size <= MaxReference && len(bytes.TrimSpace(body)) > 0
		if ok {
			if err != nil || !bytes.Equal(got, body) {
				t.Fatalf("size=%d exit=%d: want the output unchanged, got %d bytes, err %v", size, exit, len(got), err)
			}
			return
		}
		var ae *Error
		if !errors.As(err, &ae) || ae.Adapter != "assure-adapter-go" || ae.Sub != "reference" || got != nil {
			t.Fatalf("size=%d exit=%d: want *Error naming assure-adapter-go reference, got %d bytes, err %v", size, exit, len(got), err)
		}
	})
}
