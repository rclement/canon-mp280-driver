// SPDX-License-Identifier: GPL-2.0-only

package cupsbackend

import (
	"bufio"
	"io"
	"strings"
)

// FilterDiscovery reads the discovery output of the stock usb backend and
// writes the lines of wanted Canon devices again with the wrapper's URI
// scheme, e.g.
//
//	direct usb://Canon/MP280%20series?serial=X "Canon MP280 series" "Canon MP280 series USB #1" "MFG:Canon;..."
//
// becomes
//
//	direct mp280usb://Canon/MP280%20series?serial=X "Canon MP280 series" "Canon MP280 series USB #1 (with ink status)" "MFG:Canon;..."
//
// A line is kept when it is a "direct usb://Canon/" line whose text contains
// one of models (case-insensitive substring).
func FilterDiscovery(r io.Reader, w io.Writer, models []string) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		const prefix = "direct usb://"
		if !strings.HasPrefix(line, prefix) || !strings.EqualFold(line[len(prefix):min(len(prefix)+6, len(line))], "Canon/") {
			continue
		}
		if !matchesAny(line, models) {
			continue
		}
		out := "direct " + Scheme + ":" + line[len("direct usb:"):]
		out = appendHint(out, " (with ink status)")
		if _, err := io.WriteString(w, out+"\n"); err != nil {
			return err
		}
	}
	return sc.Err()
}

func matchesAny(line string, models []string) bool {
	l := strings.ToLower(line)
	for _, m := range models {
		if m != "" && strings.Contains(l, strings.ToLower(m)) {
			return true
		}
	}
	return false
}

// appendHint adds text at the end of the second quoted field (the info
// string) of a discovery line.
func appendHint(line, hint string) string {
	pos := 0
	for field := 0; field < 2; field++ {
		open := strings.IndexByte(line[pos:], '"')
		if open < 0 {
			return line
		}
		i := pos + open + 1
		for i < len(line) && line[i] != '"' {
			if line[i] == '\\' && i+1 < len(line) {
				i++
			}
			i++
		}
		if i >= len(line) {
			return line
		}
		if field == 1 {
			return line[:i] + hint + line[i:]
		}
		pos = i + 1
	}
	return line
}
