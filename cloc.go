package deptime

import "bytes"

// Cloc is a gocloc row for Go: files, and their lines split three ways.
//
// Only Go, because Go is what the graph is of, and because it is free: every
// Go blob in the history is already being read once for its imports, so its
// lines are counted in the same pass. Counting every other language would
// mean reading every other blob in the history too.
type Cloc struct {
	Files, Blank, Comment, Code int
}

func (c *Cloc) add(d Cloc) {
	c.Files += d.Files
	c.Blank += d.Blank
	c.Comment += d.Comment
	c.Code += d.Code
}

// countGo classifies a Go file's lines the way gocloc does: a blank line is
// blank wherever it is, a line inside or opening a block comment or starting
// with // is a comment, and a line with any code on it is code, comment or no.
//
// Like gocloc it reads lines, not tokens, so a "/*" inside a string literal
// can fool it. Measured against gocloc on skywire's HEAD — 2,884 files — the
// files and blank counts agree exactly, and code and comment agree on all but
// two files, both of which embed shell scripts in string literals. On one of
// them gocloc takes `"${1}"/*/` for the start of a block comment and counts a
// thousand lines of code as comment; this does not.
func countGo(src []byte) Cloc {
	c := Cloc{Files: 1}
	inBlock := false
	for len(src) > 0 {
		var line []byte
		if i := bytes.IndexByte(src, '\n'); i >= 0 {
			line, src = src[:i], src[i+1:]
		} else {
			line, src = src, nil
		}
		line = bytes.TrimSpace(line)
		switch {
		case len(line) == 0:
			c.Blank++
		case inBlock:
			end := bytes.Index(line, []byte("*/"))
			if end < 0 {
				c.Comment++
				continue
			}
			inBlock = false
			if hasCode(line[end+2:], &inBlock) {
				c.Code++
			} else {
				c.Comment++
			}
		case bytes.HasPrefix(line, []byte("//")):
			c.Comment++
		case bytes.HasPrefix(line, []byte("/*")):
			inBlock = true
			if end := bytes.Index(line[2:], []byte("*/")); end >= 0 {
				inBlock = false
				if hasCode(line[2+end+2:], &inBlock) {
					c.Code++
					continue
				}
			}
			c.Comment++
		default:
			c.Code++
			// Code that opens a block comment it does not close.
			if i := bytes.LastIndex(line, []byte("/*")); i >= 0 && !bytes.Contains(line[i:], []byte("*/")) {
				inBlock = true
			}
		}
	}
	return c
}

// hasCode reports whether what follows a closed block comment is code, and
// notes if it opens another one.
func hasCode(rest []byte, inBlock *bool) bool {
	rest = bytes.TrimSpace(rest)
	if len(rest) == 0 || bytes.HasPrefix(rest, []byte("//")) {
		return false
	}
	if bytes.HasPrefix(rest, []byte("/*")) {
		*inBlock = !bytes.Contains(rest[2:], []byte("*/"))
		return false
	}
	if i := bytes.LastIndex(rest, []byte("/*")); i >= 0 && !bytes.Contains(rest[i:], []byte("*/")) {
		*inBlock = true
	}
	return true
}
