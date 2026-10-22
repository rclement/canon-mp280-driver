// SPDX-License-Identifier: GPL-2.0-only

package filter

import (
	"bufio"
	"bytes"
	"io"
	"strings"
)

// Limits of the parser: a line chunk is at most 256 KiB-1, keys and
// values at most 255 bytes.
const (
	maxLineChunk = 1024*256 - 1
	maxKeyLen    = 255
	maxValueLen  = 255
)

// acroUtility marks the Adobe Acrobat utility procset whose
// "ct_BadResourceImplementation?" test breaks Ghostscript; the original filter
// rewrites it to "false" inside that resource.
const (
	acroUtility = "%%BeginResource: procset Adobe_CoolType_Utility_MAKEOCF"
	acroBad     = " ct_BadResourceImplementation?"
)

// Header is what the scan of the PostScript header produced.
type Header struct {
	// Params are the page device settings found in the header
	// (%%BeginFeature: *Key Value lines and <<.../Key(Value)...>>setpagedevice
	// lines), oldest first; a key seen again moves to the end with its new value.
	Params []Setting
	// Head holds every byte consumed by the scan (with the Acrobat fix
	// applied); it must be written to Ghostscript before Rest.
	Head []byte
	// Rest is the unread remainder of the input.
	Rest io.Reader
}

// ScanHeader reads the PostScript up to the end of the first page's setup and
// collects the page device settings, like the original filter.
//
// the original reader has undefined behaviour in two spots; this port defines
// them: the setpagedevice line parser stops at the end of the line (the original
// keeps scanning stale buffer memory), and a line shorter than 16 bytes never
// matches ">>setpagedevice".
func ScanHeader(in io.Reader) (*Header, error) {
	br := bufio.NewReaderSize(in, 1<<16)
	h := &Header{}
	var head bytes.Buffer
	beginPage, acro := false, false

	for {
		line, err := readLine(br)
		if len(line) == 0 {
			if err != nil && err != io.EOF {
				return nil, err
			}
			break
		}
		if strings.HasPrefix(string(line), acroUtility) {
			acro = true
		} else if acro && bytes.HasPrefix(line, []byte("%%EndResource")) {
			acro = false
		}
		if acro {
			line = fixAcrobat(line)
		}
		head.Write(line)

		// The C string the parser sees: the newline is replaced by NUL, any
		// other terminator stays.
		str := line
		if str[len(str)-1] == '\n' {
			str = str[:len(str)-1]
		}
		if z := bytes.IndexByte(str, 0); z >= 0 {
			str = str[:z]
		}
		s := string(str)

		switch {
		case strings.HasPrefix(s, "%%BeginFeature:"):
			if k, v, ok := parseBeginFeature(s[len("%%BeginFeature:"):]); ok {
				h.Params = setParam(h.Params, k, v)
			}
		case !beginPage && strings.HasPrefix(s, "%%Page:"):
			beginPage = true
		case beginPage:
			if strings.HasPrefix(s, "%%EndPageSetup") || strings.HasPrefix(s, "gsave") ||
				(len(s) > 0 && s[0] >= '0' && s[0] <= '9') {
				goto done
			}
		case len(line) >= 16 && string(line[len(line)-16:len(line)-1]) == ">>setpagedevice":
			h.Params = parseSetPageDevice(s, h.Params)
		}
		if err != nil {
			break
		}
	}
done:
	h.Head = head.Bytes()
	h.Rest = br
	return h, nil
}

// readLine returns the next line including its CR or LF terminator (a CRLF
// pair is two lines), at most maxLineChunk bytes.
func readLine(br *bufio.Reader) ([]byte, error) {
	var line []byte
	for len(line) < maxLineChunk {
		c, err := br.ReadByte()
		if err != nil {
			return line, err
		}
		line = append(line, c)
		if c == '\r' || c == '\n' {
			return line, nil
		}
	}
	return line, nil
}

// fixAcrobat replaces " ct_BadResourceImplementation?" by " false".
func fixAcrobat(line []byte) []byte {
	pat := []byte(acroBad)
	for i := 0; i+29 < len(line); {
		if bytes.HasPrefix(line[i:], pat) {
			out := append([]byte{}, line[:i]...)
			out = append(out, " false"...)
			out = append(out, line[i+len(pat):]...)
			line = out
			i += 6
		} else {
			i++
		}
	}
	return line
}

func isBlank(c byte) bool { return c == ' ' || c == '\t' }

// parseBeginFeature handles the text after "%%BeginFeature:" ("*Key Value").
func parseBeginFeature(s string) (key, value string, ok bool) {
	i := strings.IndexByte(s, '*')
	if i < 0 {
		return
	}
	s = s[i+1:]
	n := 0
	for n < len(s) && !isBlank(s[n]) && n < maxKeyLen {
		n++
	}
	key, s = s[:n], s[n:]
	for len(s) > 0 && isBlank(s[0]) {
		s = s[1:]
	}
	n = 0
	for n < len(s) && !isBlank(s[n]) && n < maxValueLen {
		n++
	}
	value = s[:n]
	return key, value, key != "" && value != ""
}

// parseSetPageDevice extracts /Key(Value) pairs from a "<<...>>setpagedevice"
// line. CNPageSizeName is stored as PageSize. Entries whose value is not in
// parentheses (arrays, names) are skipped.
func parseSetPageDevice(s string, params []Setting) []Setting {
	if len(s) < 2 {
		return params
	}
	p := s[2:] // "<<" is skipped without being checked
	isRet := func(i int) bool { return i >= len(p) || p[i] == '\r' || p[i] == '\n' }
	pos := 0
	for !isRet(pos) {
		for !isRet(pos) && p[pos] != '/' {
			pos++
		}
		if isRet(pos) {
			break
		}
		pos++ // '/'
		ks := pos
		for pos < len(p) && isAlnum(p[pos]) && pos-ks < 255 {
			pos++
		}
		key := p[ks:pos]
		if isRet(pos) || p[pos] != '(' {
			continue
		}
		pos++
		vs := pos
		for !isRet(pos) && p[pos] != ')' && !isBlank(p[pos]) && pos-vs < 255 {
			pos++
		}
		value := p[vs:pos]
		if isRet(pos) || p[pos] != ')' {
			continue
		}
		pos++
		if key == "CNPageSizeName" {
			key = "PageSize"
		}
		if key != "" {
			params = setParam(params, key, value)
		}
	}
	return params
}

func isAlnum(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

// setParam stores a parameter: an existing entry of the same key is
// removed and the new one goes to the end (it was the newest entry).
func setParam(params []Setting, key, value string) []Setting {
	for i, p := range params {
		if p.Name == key {
			params = append(params[:i], params[i+1:]...)
			break
		}
	}
	return append(params, Setting{Name: key, Value: value})
}
