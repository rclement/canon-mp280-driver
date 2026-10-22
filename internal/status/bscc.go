// SPDX-License-Identifier: GPL-2.0-only

// Package status talks the MP280's IVEC status session, decodes the BSCC
// status text the printer answers with and maps it to CUPS/IPP supply
// attributes (marker-* and printer-state-reasons).
//
// Evidence labels used in the comments: VERIFIED = seen in the recorded
// session with a real MP280 (idle, full cartridges); INFERRED = derived from
// the status vocabulary of the printer family; the IPP keyword choice is ours.
package status

import (
	"errors"
	"strconv"
	"strings"
)

// Ink is one cartridge as described by the CIR (fill) and CTK (tank state)
// fields. Colour is "BK" for the black and "CL" for the tri-colour cartridge.
type Ink struct {
	Colour   string
	Fill     string // CIR step "100" "070" "040" "010" "000" or "UK" (unknown)
	Tank     string // CTK state: SET, SETZ, NO, NS, EMP, LOW, LLOW, HRD, REG, POS1, POS2, MUL ...
	HaveFill bool
	HaveTank bool
}

// BSCC is the decoded status text "KEY:value;KEY:value;..." of a
// GetStatusResponse. The real MP280 sends, for example,
//
//	BST:00;PID:00,00,00;CHD:CLM,BKM;CTK:CL,SET,/,BK,SET;CIR:CL=100,BK=100;DJS:NO;DBS:NO;DWS:NO;DOC:4,00,NO;DSC:NO;HRI:EU;...
type BSCC struct {
	HaveBST bool
	BST     uint8 // base status: 0x80 printing, 0x20 busy, 0x08 operator call
	DBS     string
	DJS     string
	CIL     string
	HRI     string   // destination region: JP, ASI, PAM, EU, CHN
	CHD     []string // cartridge types, COLOUR FIRST: NO NS BK CL BKM CLM
	Inks    []Ink
	DWS     []string // warning codes (1499/1500: ink low)
	DOC     []string // operator-call codes (1000: no paper ...)
	DSC     string   // service-call code
	HCF     string
	Raw     string
}

// Limits copied from the fixed buffers of the original monitor; they bound the work
// done on hostile input.
const (
	maxInk   = 8
	maxCalls = 8
	maxRaw   = 2047
)

// ErrNoBST is returned when the text has no BST field, i.e. is not a BSCC
// status text at all.
var ErrNoBST = errors.New("status: text has no BST field")

func splitComma(s string) []string { return strings.Split(s, ",") }

func clip(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// ParseBSCC decodes a BSCC status text. Unknown keys are ignored.
func ParseBSCC(text string) (*BSCC, error) {
	text = clip(text, maxRaw)
	b := &BSCC{Raw: text}
	for _, field := range strings.Split(text, ";") {
		i := strings.IndexByte(field, ':')
		if i != 3 {
			continue
		}
		key, val := field[:3], field[4:]
		switch key {
		case "BST":
			n, _ := strconv.ParseUint(leadingHex(val), 16, 32)
			b.BST, b.HaveBST = uint8(n), true
		case "DBS":
			b.DBS = clip(val, 7)
		case "DJS":
			b.DJS = clip(val, 7)
		case "CIL":
			b.CIL = clip(val, 7)
		case "HRI":
			b.HRI = clip(val, 7)
		case "DSC":
			b.DSC = clip(val, 15)
		case "HCF":
			b.HCF = clip(val, 7)
		case "CHD":
			b.CHD = limit(splitComma(val), maxInk)
		case "DWS":
			b.DWS = limit(splitComma(val), maxCalls)
		case "DOC":
			b.DOC = parseDOC(val)
		case "CIR":
			b.parseCIR(val)
		case "CTK":
			b.parseCTK(val)
		}
	}
	if !b.HaveBST {
		return b, ErrNoBST
	}
	return b, nil
}

func limit(s []string, n int) []string {
	for i := range s {
		s[i] = clip(s[i], 7)
	}
	if len(s) > n {
		return s[:n]
	}
	return s
}

// leadingHex returns the longest prefix of s made of hex digits (strtoul).
func leadingHex(s string) string {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return s[:i]
		}
	}
	return s
}

// parseDOC reads "4,01,1000,/,4,01,1200": the original monitor takes every 4th
// token starting at index 2 (the code of each group); groups are separated by
// a "/" token. For a short value the last token is used.
func parseDOC(v string) []string {
	t := splitComma(v)
	var out []string
	for i := 0; i < len(t) && len(out) < maxCalls; i++ {
		if i%4 == 2 && t[i] != "/" {
			out = append(out, clip(t[i], 7))
		}
	}
	if len(out) == 0 && len(t) > 0 {
		out = append(out, clip(t[len(t)-1], 7))
	}
	return out
}

func (b *BSCC) ink(colour string, create bool) *Ink {
	for i := range b.Inks {
		if b.Inks[i].Colour == colour {
			return &b.Inks[i]
		}
	}
	if !create || len(b.Inks) >= maxInk {
		return nil
	}
	b.Inks = append(b.Inks, Ink{Colour: clip(colour, 3)})
	return &b.Inks[len(b.Inks)-1]
}

// parseCIR reads "CL=100,BK=070": the remaining-ink step of each cartridge.
func (b *BSCC) parseCIR(v string) {
	for _, t := range splitComma(v) {
		eq := strings.IndexByte(t, '=')
		if eq < 0 {
			continue
		}
		if k := b.ink(t[:eq], true); k != nil {
			k.Fill, k.HaveFill = clip(t[eq+1:], 3), true
		}
	}
}

// parseCTK reads "CL,SET,/,BK,SET": colour and tank state pairs; a one
// character token ("/" separator, a field the original monitor skips) may follow a
// pair.
func (b *BSCC) parseCTK(v string) {
	t := splitComma(v)
	for i := 0; i+1 < len(t); {
		if k := b.ink(t[i], true); k != nil {
			k.Tank, k.HaveTank = clip(t[i+1], 7), true
		}
		i += 2
		for i < len(t) && len(t[i]) <= 1 {
			i++
		}
	}
}

// Find returns the cartridge with the given colour key or nil.
func (b *BSCC) Find(colour string) *Ink { return b.ink(colour, false) }

// Printing, Busy and OperatorCall are the BST bits (lgmon's meaning).
func (b *BSCC) Printing() bool     { return b.BST&0x80 != 0 }
func (b *BSCC) Busy() bool         { return b.BST&0x20 != 0 }
func (b *BSCC) OperatorCall() bool { return b.BST&0x08 != 0 }

// CodeText gives a human meaning of a DOC/DWS code. The letters come from
// the printer's code tables; the wording is general support-code knowledge
// (INFERRED, never provoked on hardware).
func CodeText(code string) (string, bool) {
	s, ok := codeTexts[code]
	return s, ok
}

var codeTexts = map[string]string{
	"1000": "no paper / paper not loaded", "1200": "cover open", "1251": "output tray closed", "1300": "paper jam",
	"1401": "print head / cartridge problem", "1403": "print head / cartridge problem", "1485": "ink cartridge problem",
	"1682": "ink cartridge not recognized", "1684": "ink cartridge not recognized", "1686": "ink cartridge problem",
	"1687": "ink cartridge wrong position", "1688": "ink out", "1700": "waste ink absorber nearly full",
	"1701": "waste ink absorber full", "1499": "ink low (warning)", "1500": "ink low (warning)",
	"2001": "USB host problem", "2002": "USB host problem", "NO": "none", "UK": "unknown",
}

// CodeJSON is one entry of a code list in the JSON output.
type CodeJSON struct {
	Code    string  `json:"code"`
	Meaning *string `json:"meaning"`
}

// InkJSON is one cartridge in the JSON output.
type InkJSON struct {
	Colour   string `json:"color"`
	Level    *int   `json:"level"`
	LevelRaw string `json:"level_raw"`
	Tank     string `json:"tank"`
}

// JSONView is the structure `mp280-status` prints for a decoded status.
type JSONView struct {
	Region        string     `json:"region"`
	BST           uint8      `json:"bst"`
	Printing      bool       `json:"printing"`
	Busy          bool       `json:"busy"`
	OperatorCall  bool       `json:"operator_call"`
	BusyState     string     `json:"busy_state"`
	JobState      string     `json:"job_state"`
	Cartridges    []string   `json:"cartridges"`
	Ink           []InkJSON  `json:"ink"`
	Warnings      []CodeJSON `json:"warnings"`
	OperatorCalls []CodeJSON `json:"operator_calls"`
	ServiceCall   string     `json:"service_call"`
	Raw           string     `json:"raw"`
}

func codes(in []string) []CodeJSON {
	out := make([]CodeJSON, 0, len(in))
	for _, c := range in {
		e := CodeJSON{Code: c}
		if s, ok := CodeText(c); ok {
			e.Meaning = &s
		}
		out = append(out, e)
	}
	return out
}

// View converts the decoded status to its JSON form.
func (b *BSCC) View() JSONView {
	v := JSONView{
		Region: b.HRI, BST: b.BST, Printing: b.Printing(), Busy: b.Busy(), OperatorCall: b.OperatorCall(),
		BusyState: b.DBS, JobState: b.DJS, Cartridges: append([]string{}, b.CHD...),
		Ink: []InkJSON{}, Warnings: codes(b.DWS), OperatorCalls: codes(b.DOC), ServiceCall: b.DSC, Raw: b.Raw,
	}
	for _, k := range b.Inks {
		e := InkJSON{Colour: k.Colour, LevelRaw: k.Fill, Tank: k.Tank}
		if k.HaveFill && k.Fill != "UK" {
			n, _ := strconv.Atoi(k.Fill)
			e.Level = &n
		}
		v.Ink = append(v.Ink, e)
	}
	return v
}
