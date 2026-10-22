// SPDX-License-Identifier: GPL-2.0-only

package jobstream

import (
	"errors"
	"fmt"
	"time"
)

// Framing of the three kinds of command in the stream.

// appendEscParen appends "ESC ( <letter> <len:u16le> payload".
func appendEscParen(dst []byte, letter byte, payload ...byte) []byte {
	dst = append(dst, 0x1b, '(', letter, byte(len(payload)), byte(len(payload)>>8))
	return append(dst, payload...)
}

// appendEscBracket appends "ESC [ K <len:u16le> payload", the wrapper for
// language-monitor commands (SSR definition, BJL text).
func appendEscBracket(dst []byte, payload ...byte) []byte {
	dst = append(dst, 0x1b, '[', 'K', byte(len(payload)), byte(len(payload)>>8))
	return append(dst, payload...)
}

// SSRDefinition is the 16-byte command that defines the status-reply format
// ("SSR=DF;") the printer should use.
func SSRDefinition() []byte {
	p := append([]byte{0x00, 0x1e, 0x00, 0x09}, "SSR=DF;"...)
	return appendEscBracket(nil, p...)
}

// ErrBadTime is returned for a job time the original library rejects.
var ErrBadTime = errors.New("jobstream: invalid job time")

// SetTimeValue formats the 14 digits of the BJL "SetTime" value
// (YYYYMMDDhhmmss, local time). the original driver prints the year modulo 10000 and
// rejects negative years.
func SetTimeValue(t time.Time) (string, error) {
	if t.Year() < 0 {
		return "", fmt.Errorf("%w: year %d", ErrBadTime, t.Year())
	}
	return fmt.Sprintf("%04d%02d%02d%02d%02d%02d",
		t.Year()%10000, int(t.Month()), t.Day(), t.Hour(), t.Minute(), t.Second()), nil
}

// StartJobBlock is the BJL text that opens the job (72 bytes): the printer
// firmware takes the current time from it.
func StartJobBlock(t time.Time) ([]byte, error) {
	ts, err := SetTimeValue(t)
	if err != nil {
		return nil, err
	}
	out := appendEscBracket(nil, 0x00, 0x1f)
	out = append(out, "BJLSTART\nControlMode=Common\nSetTime="+ts+"\nBJLEND\n"...)
	return appendEscBracket(out, 0x00, 0x0f), nil
}

// PageHeaderPrefix are the three commands that start the first page of a job:
// ESC ( a 1, ESC ( b 1 and ESC ( q 1 (job, page and print-start markers).
func pageHeaderPrefix() []byte {
	out := appendEscParen(nil, 'a', 1)
	out = appendEscParen(out, 'b', 1)
	return appendEscParen(out, 'q', 1)
}

// PageFooter ends a page: ESC ( e with the feed command, then a form feed.
func PageFooter() []byte {
	return append(appendEscParen(nil, 'e', 0x00, 0x01), 0x0c)
}

// JobFooter closes the job: ESC ( b 0, ESC ( a 0 and the printer reset ESC @.
func JobFooter() []byte {
	out := appendEscParen(nil, 'b', 0)
	out = appendEscParen(out, 'a', 0)
	return append(out, 0x1b, '@')
}
