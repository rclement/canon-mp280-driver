// SPDX-License-Identifier: GPL-2.0-only

package status

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Reason is a bit of the printer-state-reasons keywords this driver manages.
// The backend adds AND removes exactly these nine and nothing else, so
// keywords of other origins (offline-report, connecting-to-device ...) are
// never touched.
type Reason uint

// The managed printer-state-reasons keywords.
const (
	ReasonMarkerLow   Reason = 1 << iota // marker-supply-low-warning
	ReasonMarkerEmpty                    // marker-supply-empty-error
	ReasonMediaEmpty                     // media-empty-error
	ReasonCoverOpen                      // cover-open-error
	ReasonMediaJam                       // media-jam-error
	ReasonWasteAlmost                    // marker-waste-almost-full-warning
	ReasonWasteFull                      // marker-waste-full-error
	ReasonTrayMissing                    // output-tray-missing-error
	ReasonOtherError                     // other-error
	reasonCount       = 9
)

var reasonNames = [reasonCount]string{
	"marker-supply-low-warning", "marker-supply-empty-error", "media-empty-error", "cover-open-error", "media-jam-error",
	"marker-waste-almost-full-warning", "marker-waste-full-error", "output-tray-missing-error", "other-error",
}

// Names lists the keywords set in r, in table order.
func (r Reason) Names() []string {
	out := []string{}
	for i, n := range reasonNames {
		if r&(1<<i) != 0 {
			out = append(out, n)
		}
	}
	return out
}

// Marker indexes: the order of the two markers is fixed (black first) although
// the printer lists the colour cartridge first.
const (
	MarkerBlack = 0
	MarkerColor = 1
)

// Marker is one supply as published to CUPS.
type Marker struct {
	Name    string // "Black PG-510"
	Color   string // "#000000" or "#00AEEF"
	Type    string // "ink": GNOME Settings only draws ink, toner, inkCartridge, tonerCartridge
	Level   int    // 0..100 (steps 100/70/40/10/0), -1 unknown or cartridge absent
	Present bool   // cartridge reported installed and recognised
}

// Report is the CUPS view of a status: two markers and the reasons.
type Report struct {
	Markers   [2]Marker
	Reasons   Reason
	LowLevel  int    // marker-low-levels (both)
	HighLevel int    // marker-high-levels (both)
	Activity  string // "idle", "printing", "busy" (informational only)
	Message   string // marker-message text, "" when nothing needs attention
}

// Cartridge model numbers by HRI region and CHD type
// (INFERRED from the status vocabulary; BK/CL are taken to be the larger
// cartridges).
var models = []struct{ region, bkm, clm, bk, cl string }{
	{"JP", "BC-310", "BC-311", "BC-310", "BC-311"},
	{"ASI", "PG-810", "CL-811", "PG-810XL", "CL-811XL"},
	{"PAM", "PG-210", "CL-211", "PG-210XL", "CL-211XL"},
	{"EU", "PG-510", "CL-511", "PG-512", "CL-513"},
	{"CHN", "PG-815", "CL-816", "PG-815XL", "CL-816XL"},
}

func modelOf(hri, chd string, colour bool) string {
	for _, m := range models {
		if m.region != hri {
			continue
		}
		if colour {
			switch chd {
			case "CLM":
				return m.clm
			case "CL":
				return m.cl
			}
		} else {
			switch chd {
			case "BKM":
				return m.bkm
			case "BK":
				return m.bk
			}
		}
	}
	return ""
}

func has(list []string, code string) bool {
	for _, c := range list {
		if c == code {
			return true
		}
	}
	return false
}

func isNumeric(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func atoi(s string) int {
	n := 0
	for i := 0; i < len(s); i++ {
		n = n*10 + int(s[i]-'0')
	}
	return n
}

type messages struct{ parts []string }

func (m *messages) add(s string) { m.parts = append(m.parts, s) }
func (m *messages) text() string { return strings.Join(m.parts, "; ") }
func (m *messages) contains(s string) bool {
	return strings.Contains(m.text(), s)
}

// Map converts a decoded status to the CUPS supply report.
//
// Levels: CIR carries one of 100 070 040 010 000 (or UK) per cartridge.
// VERIFIED: both read 100 on a full pair. INFERRED: quantised remaining-ink
// steps in percent. They are published as the printer reports them, with the
// low threshold at 10, so steps 010 and 000 raise marker-supply-low-warning;
// marker-supply-empty-error needs the printer to say so (tank state EMP or
// operator call 1688), never a level alone.
func Map(s *BSCC) Report {
	keys := [2]string{"BK", "CL"}
	base := [2]string{"Black", "Tri-colour"}
	r := Report{LowLevel: 10, HighLevel: 100}
	r.Markers[MarkerBlack] = Marker{Color: "#000000", Type: "ink"}
	r.Markers[MarkerColor] = Marker{Color: "#00AEEF", Type: "ink"}

	// CHD lists the COLOUR cartridge first, then the black one.
	chd := [2]string{}
	if len(s.CHD) > 1 {
		chd[0] = s.CHD[1]
	}
	if len(s.CHD) > 0 {
		chd[1] = s.CHD[0]
	}

	var msg messages
	for i := 0; i < 2; i++ {
		m := &r.Markers[i]
		ink := s.Find(keys[i])
		m.Name = base[i]
		if model := modelOf(s.HRI, chd[i], i == MarkerColor); model != "" {
			m.Name += " " + model
		}
		m.Level = -1
		m.Present = chd[i] != "NO" && chd[i] != "NS"
		tank := ""
		if ink != nil && ink.HaveTank {
			tank = ink.Tank
		}
		empty := tank == "EMP"
		lowTank := tank == "LOW" || tank == "LLOW"
		switch tank { // missing, not recognised, mis-positioned or duplicated tank
		case "NO", "NS", "HRD", "REG", "POS1", "POS2", "MUL":
			m.Present = false
		}
		if ink != nil && ink.HaveFill && isNumeric(ink.Fill) {
			m.Level = atoi(ink.Fill)
		}
		if !m.Present {
			m.Level = -1
		}
		if empty {
			m.Level = 0
			r.Reasons |= ReasonMarkerEmpty
			msg.add(m.Name + " is empty")
		} else if lowTank || (m.Present && m.Level >= 0 && m.Level <= r.LowLevel) {
			r.Reasons |= ReasonMarkerLow
			msg.add(m.Name + " is low")
		}
		if !m.Present {
			r.Reasons |= ReasonOtherError
			msg.add(m.Name + " is missing or not recognised")
		}
	}

	// DWS: ink-low warnings that the tank states did not already show.
	if has(s.DWS, "1499") || has(s.DWS, "1500") {
		r.Reasons |= ReasonMarkerLow
	}

	// DOC operator calls.
	for _, c := range s.DOC {
		switch c {
		case "NO", "UK", "":
		case "1000":
			r.Reasons |= ReasonMediaEmpty
			msg.add("Out of paper")
		case "1300":
			r.Reasons |= ReasonMediaJam
			msg.add("Paper jam")
		case "1200":
			r.Reasons |= ReasonCoverOpen
			msg.add("Cover open")
		case "1251":
			r.Reasons |= ReasonTrayMissing
			msg.add("Paper output tray is closed")
		case "1688":
			r.Reasons |= ReasonMarkerEmpty
			if !msg.contains("is empty") {
				msg.add("Out of ink")
			}
		case "1700", "1701":
			r.Reasons |= ReasonWasteAlmost
			msg.add("Ink absorber almost full")
		case "1401", "1403", "1682", "1684", "1485":
			r.Reasons |= ReasonOtherError
			if !msg.contains("missing") {
				msg.add("Ink cartridge missing or not recognised")
			}
		case "1686":
			r.Reasons |= ReasonOtherError
			msg.add("Ink may have run out, level unknown")
		case "1687":
			r.Reasons |= ReasonOtherError
			msg.add("Ink cartridge not installed properly")
		default:
			r.Reasons |= ReasonOtherError
			msg.add("Printer needs attention")
		}
	}

	// DSC service calls are unrecoverable; 5B00/5B01 = ink absorber full.
	if s.DSC != "" && s.DSC != "NO" && s.DSC != "UK" {
		if s.DSC == "5B00" || s.DSC == "5B01" {
			r.Reasons |= ReasonWasteFull
			msg.add("Ink absorber full, service required")
		} else {
			r.Reasons |= ReasonOtherError
			msg.add("Printer service error")
		}
	}
	r.Message = msg.text()

	switch {
	case s.Printing():
		r.Activity = "printing"
	case s.Busy() || (s.DBS != "" && s.DBS != "NO" && s.DBS != "UK" && s.DBS != "RS"):
		r.Activity = "busy"
	default:
		r.Activity = "idle"
	}
	return r
}

// quoted writes a value in CUPS option syntax: double quoted, \ and "
// escaped.
func quoted(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		if s[i] == '"' || s[i] == '\\' {
			b.WriteByte('\\')
		}
		b.WriteByte(s[i])
	}
	b.WriteByte('"')
	return b.String()
}

// FormatAttr is the one "ATTR:" line (without newline) that publishes all
// marker-* attributes.
func (r Report) FormatAttr() string {
	m := r.Markers
	return fmt.Sprintf("ATTR: marker-colors=%s,%s marker-high-levels=%d,%d marker-levels=%d,%d marker-low-levels=%d,%d marker-message=%s marker-names=\"%s,%s\" marker-types=%s,%s",
		m[0].Color, m[1].Color, r.HighLevel, r.HighLevel, m[0].Level, m[1].Level, r.LowLevel, r.LowLevel,
		quoted(r.Message), m[0].Name, m[1].Name, m[0].Type, m[1].Type)
}

// FormatState returns the "STATE:" lines, one keyword per line (unambiguous
// for every cupsd version): all additions first, then all removals.
func (r Report) FormatState() []string {
	var out []string
	for _, add := range []bool{true, false} {
		for i, n := range reasonNames {
			if (r.Reasons&(1<<i) != 0) == add {
				sign := "-"
				if add {
					sign = "+"
				}
				out = append(out, "STATE: "+sign+n)
			}
		}
	}
	return out
}

// CupsLines returns ATTR and STATE lines in the order the backend prints them.
func (r Report) CupsLines() []string {
	return append([]string{r.FormatAttr()}, r.FormatState()...)
}

// reportJSON is the shape of `mp280-status --report`.
type reportJSON struct {
	Activity  string       `json:"activity"`
	Markers   []markerJSON `json:"markers"`
	LowLevel  int          `json:"low_level"`
	HighLevel int          `json:"high_level"`
	Reasons   []string     `json:"reasons"`
	Message   string       `json:"message"`
}

type markerJSON struct {
	Name    string `json:"name"`
	Color   string `json:"color"`
	Type    string `json:"type"`
	Level   int    `json:"level"`
	Present bool   `json:"present"`
}

// JSON renders the report as one JSON object.
func (r Report) JSON() ([]byte, error) {
	j := reportJSON{Activity: r.Activity, LowLevel: r.LowLevel, HighLevel: r.HighLevel, Reasons: r.Reasons.Names(), Message: r.Message}
	for _, m := range r.Markers {
		j.Markers = append(j.Markers, markerJSON(m))
	}
	return json.Marshal(j)
}
