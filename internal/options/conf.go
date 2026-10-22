// SPDX-License-Identifier: GPL-2.0-only

package options

import (
	"bytes"
	"os"
	"strings"
)

// Conf is the front end's resource file (cif<model>.conf): per section, a list
// of "name number" lines that give the numeric ids of the words accepted on the
// command line (media names, paper sizes, quality numbers ...) and the model
// ids.
type Conf struct {
	sections map[string][][]string // section -> lines -> whitespace separated tokens
}

// LoadConf reads a resource file.
func LoadConf(path string) (*Conf, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseConf(b), nil
}

// ParseConf parses resource file contents. The format is "[section]"
// lines, "#" comments, tokens separated by blanks or tabs.
func ParseConf(b []byte) *Conf {
	c := &Conf{sections: map[string][][]string{}}
	section := ""
	inSection := false
	for _, line := range bytes.Split(b, []byte{'\n'}) {
		s := strings.TrimRight(string(line), "\r")
		if strings.HasPrefix(s, "[") {
			// The tag name ends at the first ']' with blanks removed.
			name := strings.NewReplacer(" ", "", "\t", "").Replace(s[1:])
			if i := strings.IndexByte(name, ']'); i >= 0 {
				name = name[:i]
			}
			section, inSection = name, true
			if _, ok := c.sections[section]; !ok {
				c.sections[section] = nil
			}
			continue
		}
		if !inSection {
			continue
		}
		fields := func(s string) []string {
			return strings.FieldsFunc(s, func(r rune) bool { return r == ' ' || r == '\t' })
		}
		if i := strings.IndexByte(s, '#'); i >= 0 {
			// the original reader swallows a comment together with its line end,
			// so a comment-only line is not a (blank) line for it.
			if f := fields(s[:i]); len(f) > 0 {
				c.sections[section] = append(c.sections[section], f)
			}
			continue
		}
		c.sections[section] = append(c.sections[section], fields(s))
	}
	return c
}

// ID looks up the number for a name in a section. The first line whose first
// token equals name wins; a line with three or more tokens is a format error
// that ends the search without a result, as in the original reader.
// Quirk kept: the original reader never clears the pointers to the previous line,
// so an empty name matches the first blank line that follows a two-token
// line and yields the id of that earlier line.
func (c *Conf) ID(section, name string) (int, bool) {
	stale := ""
	for _, tok := range c.sections[section] {
		if len(tok) >= 3 {
			return 0, false
		}
		if len(tok) == 0 && name == "" && stale != "" {
			return atoi(stale), true
		}
		if len(tok) == 2 {
			stale = tok[1]
			if tok[0] == name {
				return atoi(tok[1]), true
			}
		}
	}
	return 0, false
}

// atoi is C's atoi: leading blanks, a sign, digits; anything else stops it.
func atoi(s string) int {
	s = strings.TrimLeft(s, " \t\n\v\f\r")
	neg := false
	if s != "" && (s[0] == '-' || s[0] == '+') {
		neg = s[0] == '-'
		s = s[1:]
	}
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			break
		}
		n = n*10 + int(r-'0')
	}
	if neg {
		return -n
	}
	return n
}
