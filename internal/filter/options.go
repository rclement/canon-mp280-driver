// SPDX-License-Identifier: GPL-2.0-only

// Package filter is the CUPS filter: the CUPS filter together
// with the Ghostscript glue and the in-process cif front end.
//
//	CUPS: argv job user title copies options [file], env PPD, PostScript in
//	  -> scan the PostScript header for page device settings (psscan.go)
//	  -> map PPD choices and job options to cif options        (params.go)
//	  -> Ghostscript renders PPM pages                          (run.go)
//	  -> cif.Run turns the pages into the printer stream
package filter

import (
	"strings"
)

// ParseOptions parses a CUPS option string the way cupsParseOptions does:
// "name=value" pairs separated by blanks, quoting with ' or " and backslash,
// bare "name" meaning true and "noname" meaning false. The result is sorted
// by name (case-insensitively) and later duplicates replace earlier ones,
// exactly like the array cupsAddOption builds.
func ParseOptions(s string) []Setting {
	var opts []Setting
	i := 0
	for {
		for i < len(s) && isSpace(s[i]) {
			i++
		}
		if i >= len(s) {
			break
		}
		start := i
		for i < len(s) && !isSpace(s[i]) && s[i] != '=' {
			i++
		}
		name := s[start:i]
		j := i
		for j < len(s) && isSpace(s[j]) {
			j++
		}
		if j >= len(s) || s[j] != '=' {
			i = j
			if len(name) >= 2 && strings.EqualFold(name[:2], "no") {
				opts = AddOption(opts, name[2:], "false")
			} else {
				opts = AddOption(opts, name, "true")
			}
			continue
		}
		i = j + 1 // skip '='
		var val []byte
		for i < len(s) && !isSpace(s[i]) {
			switch c := s[i]; c {
			case '\\':
				i++
				if i < len(s) {
					val = append(val, s[i])
					i++
				}
			case '\'', '"':
				i++
				for i < len(s) && s[i] != c {
					if s[i] == '\\' && i+1 < len(s) {
						i++
					}
					val = append(val, s[i])
					i++
				}
				if i < len(s) {
					i++
				}
			default:
				val = append(val, c)
				i++
			}
		}
		opts = AddOption(opts, name, string(val))
	}
	return opts
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v' || c == '\f'
}

// AddOption is cupsAddOption: keeps the list sorted by case-insensitive name
// and replaces the value of an existing name. An empty name is ignored.
func AddOption(opts []Setting, name, value string) []Setting {
	if name == "" {
		return opts
	}
	lo := 0
	for lo < len(opts) {
		c := compareFold(opts[lo].Name, name)
		if c == 0 {
			opts[lo].Value = value
			return opts
		}
		if c > 0 {
			break
		}
		lo++
	}
	opts = append(opts, Setting{})
	copy(opts[lo+1:], opts[lo:])
	opts[lo] = Setting{Name: name, Value: value}
	return opts
}

func compareFold(a, b string) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		x, y := lower(a[i]), lower(b[i])
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	switch {
	case len(a) < len(b):
		return -1
	case len(a) > len(b):
		return 1
	}
	return 0
}

func lower(c byte) byte {
	if c >= 'A' && c <= 'Z' {
		return c + 'a' - 'A'
	}
	return c
}

// GetOption returns the value of a (case-insensitively named) option.
func GetOption(opts []Setting, name string) (string, bool) {
	for _, o := range opts {
		if strings.EqualFold(o.Name, name) {
			return o.Value, true
		}
	}
	return "", false
}
