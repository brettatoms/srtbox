package config

import (
	"bytes"
	"fmt"
	"slices"
)

// jsonc turns JSON with comments and trailing commas into plain JSON. It
// blanks out what it drops rather than removing it, so an offset in a parse
// error still points into the original file.
func jsonc(b []byte) ([]byte, error) {
	out := slices.Clone(b)
	// Comments first, so that a comment between a comma and its closing
	// bracket is already whitespace when the commas are checked.
	for i := 0; i < len(out); i++ {
		switch {
		case out[i] == '"':
			i = stringEnd(out, i)
		case bytes.HasPrefix(out[i:], []byte("//")):
			end := bytes.IndexByte(out[i:], '\n')
			if end < 0 {
				end = len(out) - i
			}
			blank(out[i : i+end])
			i += end
		case bytes.HasPrefix(out[i:], []byte("/*")):
			end := bytes.Index(out[i+2:], []byte("*/"))
			if end < 0 {
				return nil, fmt.Errorf("line %d: unterminated /* comment", lineOf(b, i))
			}
			blank(out[i : i+2+end+2])
			i += 2 + end + 1
		}
	}
	for i := 0; i < len(out); i++ {
		switch out[i] {
		case '"':
			i = stringEnd(out, i)
		case ',':
			next := bytes.TrimLeft(out[i+1:], " \t\r\n")
			if len(next) > 0 && (next[0] == '}' || next[0] == ']') {
				out[i] = ' '
			}
		}
	}
	return out, nil
}

// stringEnd returns the index of the quote closing the string that opens at
// b[start], or the last index if the string never closes.
func stringEnd(b []byte, start int) int {
	for i := start + 1; i < len(b); i++ {
		switch b[i] {
		case '\\':
			i++
		case '"':
			return i
		}
	}
	return len(b) - 1
}

// blank overwrites b with spaces, keeping newlines so line numbers hold.
func blank(b []byte) {
	for i, c := range b {
		if c != '\n' {
			b[i] = ' '
		}
	}
}

// lineOf returns the 1-based line holding byte offset off of b.
func lineOf(b []byte, off int) int {
	return 1 + bytes.Count(b[:min(off, len(b))], []byte("\n"))
}
