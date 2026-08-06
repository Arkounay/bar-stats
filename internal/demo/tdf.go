package demo

import (
	"strconv"
	"strings"
)

// tdfSection is a parsed node of Spring's TDF configuration format, the
// plain-text format used for the start script embedded in every demo:
//
//	[GAME]
//	{
//	    mapname=Rosetta 1.4.4;
//	    [PLAYER0]
//	    {
//	        name=SomePlayer;
//	        team=3;
//	    }
//	}
//
// Keys are lower-cased on parse; Spring itself treats them case-insensitively
// and BAR is inconsistent about casing.
type tdfSection struct {
	keys     map[string]string
	children map[string]*tdfSection
}

func newTDFSection() *tdfSection {
	return &tdfSection{
		keys:     make(map[string]string),
		children: make(map[string]*tdfSection),
	}
}

// parseTDF parses a TDF document.
//
// It scans by token rather than by line, because the delimiters are not
// line-bound: lobbies emit both the expanded form and one-liners such as
// `[ALLYTEAM0] { numallies=0; }`, and both must parse the same way.
//
// Parsing is deliberately forgiving. The start script is written by several
// different lobby versions, and a fragment this decoder does not recognise
// should cost that one value rather than the whole match's metadata.
func parseTDF(src string) *tdfSection {
	root := newTDFSection()
	stack := []*tdfSection{root}
	// pendingName holds the most recent [SECTION] header, which the next '{'
	// binds to.
	var pendingName string

	for i := 0; i < len(src); {
		c := src[i]
		switch {
		case c == ' ' || c == '\t' || c == '\r' || c == '\n':
			i++

		case c == '/' && i+1 < len(src) && src[i+1] == '/':
			i = skipTo(src, i, '\n')

		case c == '[':
			end := strings.IndexByte(src[i:], ']')
			if end < 0 {
				return root // unterminated header; keep what we have
			}
			pendingName = strings.ToLower(strings.TrimSpace(src[i+1 : i+end]))
			i += end + 1

		case c == '{':
			parent := stack[len(stack)-1]
			child, ok := parent.children[pendingName]
			if !ok {
				child = newTDFSection()
				parent.children[pendingName] = child
			}
			stack = append(stack, child)
			pendingName = ""
			i++

		case c == '}':
			if len(stack) > 1 {
				stack = stack[:len(stack)-1]
			}
			i++

		default:
			// A key/value pair, terminated by ';' or the end of the line.
			end := i
			for end < len(src) && src[end] != ';' && src[end] != '\n' && src[end] != '}' {
				end++
			}
			if key, value, ok := strings.Cut(src[i:end], "="); ok {
				section := stack[len(stack)-1]
				section.keys[strings.ToLower(strings.TrimSpace(key))] = strings.TrimSpace(value)
			}
			// Leave a closing brace for the next iteration to handle.
			if end < len(src) && src[end] == '}' {
				i = end
			} else {
				i = end + 1
			}
		}
	}
	return root
}

// skipTo returns the index just past the next occurrence of b, or the end of
// the string.
func skipTo(src string, from int, b byte) int {
	if idx := strings.IndexByte(src[from:], b); idx >= 0 {
		return from + idx + 1
	}
	return len(src)
}

func (s *tdfSection) child(name string) *tdfSection {
	if s == nil {
		return nil
	}
	return s.children[strings.ToLower(name)]
}

func (s *tdfSection) str(key string) string {
	if s == nil {
		return ""
	}
	return s.keys[strings.ToLower(key)]
}

func (s *tdfSection) intOr(key string, fallback int) int {
	v, err := strconv.Atoi(strings.TrimSpace(s.str(key)))
	if err != nil {
		return fallback
	}
	return v
}

func (s *tdfSection) boolOr(key string, fallback bool) bool {
	switch strings.TrimSpace(s.str(key)) {
	case "1", "true", "True":
		return true
	case "0", "false", "False":
		return false
	}
	return fallback
}

// numberedChildren returns the sections named prefix+"0", prefix+"1", … in
// index order, stopping at the first gap. Spring numbers players, teams and
// ally teams contiguously from zero.
func (s *tdfSection) numberedChildren(prefix string) []*tdfSection {
	var out []*tdfSection
	for i := 0; ; i++ {
		c := s.child(prefix + strconv.Itoa(i))
		if c == nil {
			return out
		}
		out = append(out, c)
	}
}
