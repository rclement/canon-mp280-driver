// SPDX-License-Identifier: GPL-2.0-only

package options

import (
	"errors"
	"fmt"
	"strings"
)

// argKind says what a command-line option takes.
type argKind int

const (
	argNone argKind = iota
	argString
	argInt
)

// optionSpec describes one command-line option.
type optionSpec struct {
	name  string
	short byte
	kind  argKind
}

// optionTable lists the options of the front end (cif).
var optionTable = []optionSpec{
	{"location", 0, argString}, {"cartridge", 'c', argString}, {"media", 'm', argString}, {"quality", 'q', argString},
	{"grayscale", 'g', argNone}, {"halftoning", 0, argString}, {"paperload", 0, argString},
	{"correctionmode", 0, argString}, {"renderintent", 0, argString}, {"gamma", 0, argString},
	{"imagesource", 0, argString}, {"balance_c", 0, argInt}, {"balance_m", 0, argInt}, {"balance_y", 0, argInt},
	{"balance_k", 0, argInt}, {"density", 0, argInt}, {"papersize", 'p', argString}, {"imageres", 0, argInt},
	{"display", 0, argString}, {"fit", 0, argNone}, {"internalversion", 0, argNone}, {"borderless", 0, argNone},
	{"bbox", 0, argString}, {"extension", 0, argInt}, {"percent", 0, argInt}, {"copies", 0, argInt},
	{"revprint", 0, argNone}, {"collate", 0, argNone}, {"full", 0, argNone}, {"paperwidth", 0, argString},
	{"paperheight", 0, argString}, {"papergap", 0, argString}, {"help", 0, argNone}, {"duplex", 0, argNone},
	{"stapleside", 0, argString}, {"inkcartridgesettings", 0, argString}, {"contrast", 0, argInt},
	{"tone", 0, argInt}, {"bidi", 0, argNone}, {"supportnetwork", 0, argNone},
}

// rawOptions holds what the command line said: for each option given, its last
// value.
type rawOptions struct {
	set   map[string]bool
	strs  map[string]string
	ints  map[string]int
	extra []string // positional arguments
}

// ErrUsage wraps command-line syntax errors (unknown option, missing or
// malformed argument), which the original reports through its option library.
var ErrUsage = errors.New("options: usage error")

func lookupLong(name string) (optionSpec, bool) {
	for _, o := range optionTable {
		if o.name == name {
			return o, true
		}
	}
	return optionSpec{}, false
}

func lookupShort(c byte) (optionSpec, bool) {
	for _, o := range optionTable {
		if o.short == c {
			return o, true
		}
	}
	return optionSpec{}, false
}

// parseInt converts an option argument like C's strtol(s, &end, 0) followed by
// "*end must be NUL" and an int range check, as popt does.
func parseInt(s string) (int, error) {
	i := 0
	for i < len(s) && (s[i] == ' ' || (s[i] >= '\t' && s[i] <= '\r')) {
		i++
	}
	if i == len(s) {
		if s == "" {
			return 0, nil
		}
		return 0, fmt.Errorf("%w: %q is not a number", ErrUsage, s)
	}
	neg := false
	if s[i] == '+' || s[i] == '-' {
		neg = s[i] == '-'
		i++
	}
	base := 10
	if i+2 < len(s) && s[i] == '0' && (s[i+1] == 'x' || s[i+1] == 'X') && isHex(s[i+2]) {
		base, i = 16, i+2
	} else if i < len(s) && s[i] == '0' {
		base = 8
	}
	start := i
	var v int64
	for ; i < len(s); i++ {
		d := digitVal(s[i])
		if d < 0 || d >= base {
			break
		}
		v = v*int64(base) + int64(d)
		if v > 1<<40 {
			return 0, fmt.Errorf("%w: %q out of range", ErrUsage, s)
		}
	}
	if i == start && !(base == 8) {
		return 0, fmt.Errorf("%w: %q is not a number", ErrUsage, s)
	}
	if i != len(s) {
		return 0, fmt.Errorf("%w: %q is not a number", ErrUsage, s)
	}
	if neg {
		v = -v
	}
	if v > 2147483647 || v < -2147483648 {
		return 0, fmt.Errorf("%w: %q out of range", ErrUsage, s)
	}
	return int(v), nil
}

func isHex(c byte) bool { return digitVal(c) >= 0 }

func digitVal(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	}
	return -1
}

// parseArgs splits a command line (without the program name) like popt: long
// options as --name value or --name=value, short options (-c -m -q -g -p,
// possibly clustered, value attached or next), "--" ends options, everything
// else is positional.
func parseArgs(args []string) (*rawOptions, error) {
	r := &rawOptions{set: map[string]bool{}, strs: map[string]string{}, ints: map[string]int{}}
	store := func(o optionSpec, val string, hasVal bool) error {
		switch o.kind {
		case argNone:
			if hasVal {
				return fmt.Errorf("%w: option --%s takes no argument", ErrUsage, o.name)
			}
		case argString:
			if !hasVal {
				return fmt.Errorf("%w: option --%s needs an argument", ErrUsage, o.name)
			}
			r.strs[o.name] = val
		case argInt:
			if !hasVal {
				return fmt.Errorf("%w: option --%s needs an argument", ErrUsage, o.name)
			}
			n, err := parseInt(val)
			if err != nil {
				return err
			}
			r.ints[o.name] = n
		}
		r.set[o.name] = true
		return nil
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			r.extra = append(r.extra, args[i+1:]...)
			return r, nil
		case strings.HasPrefix(a, "--"):
			name, val, hasVal := strings.Cut(a[2:], "=")
			o, ok := lookupLong(name)
			if !ok {
				return nil, fmt.Errorf("%w: unknown option %s", ErrUsage, a)
			}
			if !hasVal && o.kind != argNone {
				if i+1 >= len(args) {
					return nil, fmt.Errorf("%w: option --%s needs an argument", ErrUsage, o.name)
				}
				i++
				val, hasVal = args[i], true
			}
			if err := store(o, val, hasVal); err != nil {
				return nil, err
			}
		case len(a) > 1 && a[0] == '-':
			for j := 1; j < len(a); j++ {
				o, ok := lookupShort(a[j])
				if !ok {
					return nil, fmt.Errorf("%w: unknown option -%c", ErrUsage, a[j])
				}
				if o.kind == argNone {
					if err := store(o, "", false); err != nil {
						return nil, err
					}
					continue
				}
				val := a[j+1:]
				if val == "" {
					if i+1 >= len(args) {
						return nil, fmt.Errorf("%w: option -%c needs an argument", ErrUsage, a[j])
					}
					i++
					val = args[i]
				}
				if err := store(o, val, true); err != nil {
					return nil, err
				}
				break
			}
		default:
			r.extra = append(r.extra, a)
		}
	}
	return r, nil
}
