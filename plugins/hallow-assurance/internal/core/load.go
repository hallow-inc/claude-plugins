package core

import (
	"errors"
	"fmt"
	"strings"

	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/internal/decode"
	"github.com/hallow-inc/claude-plugins/plugins/hallow-assurance/schemas"
)

type LoadError struct {
	File     string
	Problems []schemas.Violation
}

func (e *LoadError) Error() string {
	lines := make([]string, len(e.Problems))
	for i, p := range e.Problems {
		lines[i] = e.File + ": " + p.String()
	}
	return strings.Join(lines, "\n")
}

func validated(k schemas.Kind, file string, data []byte) (decode.Doc, error) {
	vs, err := schemas.Validate(k, data)
	if err != nil {
		if de, ok := errors.AsType[*decode.Error](err); ok {
			return decode.Doc{}, &LoadError{File: file, Problems: []schemas.Violation{{Line: de.Line, Column: de.Column, Keyword: "syntax", Message: de.Msg}}}
		}
		return decode.Doc{}, fmt.Errorf("%s: %w", file, err)
	}
	if len(vs) > 0 {
		return decode.Doc{}, &LoadError{File: file, Problems: vs}
	}
	if k == schemas.Manifest || k == schemas.Catalog || k == schemas.Waivers {
		return decode.YAML(data)
	}
	return decode.JSON(data)
}

type problems struct {
	doc decode.Doc
	vs  []schemas.Violation
}

func (p *problems) add(ptr, keyword, format string, args ...any) {
	pos := p.doc.Pos[ptr]
	p.vs = append(p.vs, schemas.Violation{Line: pos.Line, Column: pos.Column, Location: ptr, Keyword: keyword, Message: fmt.Sprintf(format, args...)})
}

func (p *problems) err(file string) error {
	if len(p.vs) == 0 {
		return nil
	}
	return &LoadError{File: file, Problems: p.vs}
}
