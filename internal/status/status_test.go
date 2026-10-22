// SPDX-License-Identifier: GPL-2.0-only

package status_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"canon-mp280-driver/internal/jobstream"
	"canon-mp280-driver/internal/status"
	"canon-mp280-driver/test/mockprinter"
)

const testdata = "../../test/status/testdata"

func readFile(t testing.TB, rel string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(testdata, rel))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// scenario returns the BSCC line of a scenario file.
func scenario(t testing.TB, name string) string {
	t.Helper()
	for _, l := range strings.Split(string(readFile(t, "scenarios/"+name+".bscc")), "\n") {
		if l != "" && !strings.HasPrefix(l, "#") {
			return l
		}
	}
	t.Fatal("empty scenario " + name)
	return ""
}

func scenarioNames(t testing.TB) []string {
	t.Helper()
	m, _ := filepath.Glob(filepath.Join(testdata, "scenarios", "*.bscc"))
	if len(m) != 15 {
		t.Fatalf("expected 15 scenarios, found %d", len(m))
	}
	var out []string
	for _, p := range m {
		out = append(out, strings.TrimSuffix(filepath.Base(p), ".bscc"))
	}
	return out
}

const (
	head = "BST:00;PID:00,00,00;CHD:CLM,BKM;LVR:GAL,AT;MID:XXXXXX;"
	tail = "FSI:00;HRI:EU;HCF:AL;MSI:DAT,E3,HASF;PDR:4;HRC:NO;"
)

func mapMid(t *testing.T, mid string) (*status.BSCC, status.Report) {
	t.Helper()
	s, err := status.ParseBSCC(head + mid + tail)
	if err != nil {
		t.Fatal(err)
	}
	return s, status.Map(s)
}

func idleMid(ink, doc string) string {
	return "CTK:CL,SET,/,BK,SET;CIR:" + ink + ";DJS:NO;DBS:NO;DWS:NO;DOC:" + doc + ";DSC:NO;"
}

func TestMapUnit(t *testing.T) {
	// idle, real sample
	s, r := mapMid(t, idleMid("CL=100,BK=100", "4,00,NO"))
	eq := func(name string, got, want any) {
		t.Helper()
		if got != want {
			t.Errorf("%s = %v, want %v", name, got, want)
		}
	}
	eq("nink", len(s.Inks), 2)
	eq("ink0 colour", s.Inks[0].Colour, "CL")
	eq("ink0 tank", s.Inks[0].Tank, "SET")
	eq("levels", [2]int{r.Markers[0].Level, r.Markers[1].Level}, [2]int{100, 100})
	eq("reasons", r.Reasons, status.Reason(0))
	eq("name0", r.Markers[0].Name, "Black PG-510")
	eq("name1", r.Markers[1].Name, "Tri-colour CL-511")
	eq("activity", r.Activity, "idle")
	eq("doc", strings.Join(s.DOC, ","), "NO")
	eq("attr", r.FormatAttr(), `ATTR: marker-colors=#000000,#00AEEF marker-high-levels=100,100 marker-levels=100,100 `+
		`marker-low-levels=10,10 marker-message="" marker-names="Black PG-510,Tri-colour CL-511" marker-types=ink,ink`)
	st := r.FormatState()
	eq("state first", strings.Join(st[:3], "|"), "STATE: -marker-supply-low-warning|STATE: -marker-supply-empty-error|STATE: -media-empty-error")
	for _, l := range st {
		if strings.HasPrefix(l, "STATE: +") {
			t.Errorf("unexpected %s", l)
		}
	}

	cases := []struct {
		name, ink, doc, ctk string
		want                status.Reason
	}{
		{"fixed marker order, 70/40", "CL=040,BK=070", "4,00,NO", "", 0},
		{"step 010 alone is low", "CL=100,BK=010", "4,00,NO", "", status.ReasonMarkerLow},
		{"step 040 is not low", "CL=100,BK=040", "4,00,NO", "", 0},
		{"no paper", "CL=100,BK=100", "4,01,1000", "", status.ReasonMediaEmpty},
		{"cover open", "CL=100,BK=100", "4,01,1200", "", status.ReasonCoverOpen},
		{"paper jam", "CL=100,BK=100", "4,01,1300", "", status.ReasonMediaJam},
		{"tray closed", "CL=100,BK=100", "4,01,1251", "", status.ReasonTrayMissing},
		{"waste almost full", "CL=100,BK=100", "4,01,1700", "", status.ReasonWasteAlmost},
		{"two calls", "CL=100,BK=100", "4,01,1000,/,4,01,1200", "", status.ReasonMediaEmpty | status.ReasonCoverOpen},
		{"unknown call", "CL=100,BK=100", "4,01,9999", "", status.ReasonOtherError},
		{"service call 5100", "CL=100,BK=100", "4,00,NO", "5100", status.ReasonOtherError},
		{"service call 5B00", "CL=100,BK=100", "4,00,NO", "5B00", status.ReasonWasteFull},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mid := idleMid(c.ink, c.doc)
			if c.ctk != "" {
				mid = strings.Replace(mid, "DSC:NO", "DSC:"+c.ctk, 1)
			}
			_, r := mapMid(t, mid)
			if r.Reasons != c.want {
				t.Errorf("reasons %v, want %v", r.Reasons.Names(), c.want.Names())
			}
		})
	}

	// order Black, Colour whatever the order in CIR
	_, r = mapMid(t, idleMid("CL=040,BK=070", "4,00,NO"))
	eq("black level", r.Markers[status.MarkerBlack].Level, 70)
	eq("colour level", r.Markers[status.MarkerColor].Level, 40)

	// low: step 010, tank LOW + DWS 1500
	_, r = mapMid(t, "CTK:CL,LOW,/,BK,SET;CIR:CL=010,BK=070;DJS:NO;DBS:NO;DWS:1500;DOC:4,00,NO;DSC:NO;")
	eq("low reasons", r.Reasons, status.ReasonMarkerLow)
	eq("low level", r.Markers[1].Level, 10)
	if !strings.Contains(r.Message, "Tri-colour CL-511 is low") {
		t.Errorf("message %q", r.Message)
	}
	st = r.FormatState()
	eq("low state", st[0]+"|"+st[1], "STATE: +marker-supply-low-warning|STATE: -marker-supply-empty-error")

	// empty black
	s, r = mapMid(t, "CTK:CL,SET,/,BK,EMP;CIR:CL=070,BK=000;DJS:NO;DBS:NO;DWS:NO;DOC:4,01,1688;DSC:NO;")
	eq("empty reasons", r.Reasons, status.ReasonMarkerEmpty)
	eq("empty levels", [2]int{r.Markers[0].Level, r.Markers[1].Level}, [2]int{0, 70})
	eq("empty doc", s.DOC[0], "1688")

	// missing cartridge
	_, r = mapMid(t, "CTK:CL,SET,/,BK,NO;CIR:CL=100,BK=UK;DJS:NO;DBS:NO;DWS:NO;DOC:4,01,1401;DSC:NO;")
	eq("missing other-error", r.Reasons&status.ReasonOtherError != 0, true)
	eq("missing level", r.Markers[0].Level, -1)
	eq("missing present", r.Markers[0].Present, false)
	eq("other present", r.Markers[1].Present, true)

	// unknown level
	_, r = mapMid(t, "CTK:CL,SETZ,/,BK,SETZ;CIR:CL=UK,BK=UK;DJS:NO;DBS:NO;DWS:NO;DOC:4,00,NO;DSC:NO;")
	eq("unknown levels", [2]int{r.Markers[0].Level, r.Markers[1].Level}, [2]int{-1, -1})
	eq("unknown reasons", r.Reasons, status.Reason(0))

	// two operator calls decode
	s, _ = mapMid(t, idleMid("CL=100,BK=100", "4,01,1000,/,4,01,1200"))
	eq("two docs", strings.Join(s.DOC, ","), "1000,1200")

	// activity
	for _, c := range []struct{ text, want string }{
		{"BST:80;DJS:PR;CIR:CL=100,BK=100;", "printing"},
		{"BST:20;DBS:CL;CIR:CL=100,BK=100;", "busy"},
	} {
		s, err := status.ParseBSCC(c.text)
		if err != nil {
			t.Fatal(err)
		}
		eq("activity "+c.text, status.Map(s).Activity, c.want)
	}

	// regions
	s, _ = status.ParseBSCC("BST:00;CHD:CL,BK;HRI:EU;CIR:CL=100,BK=100;")
	r = status.Map(s)
	eq("EU large black", r.Markers[0].Name, "Black PG-512")
	eq("EU large colour", r.Markers[1].Name, "Tri-colour CL-513")
	s, _ = status.ParseBSCC("BST:00;CHD:CLM,BKM;HRI:ZZZ;CIR:CL=100,BK=100;")
	r = status.Map(s)
	eq("unknown region black", r.Markers[0].Name, "Black")
	eq("unknown region colour", r.Markers[1].Name, "Tri-colour")

	// parser robustness
	for _, bad := range []string{"", "garbage"} {
		if _, err := status.ParseBSCC(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if s, err := status.ParseBSCC("BST:00;CIR:CL=100,BK;CTK:,;DOC:;DWS:;"); err != nil {
		t.Error(err)
	} else {
		status.Map(s)
	}
}

func TestGolden(t *testing.T) {
	for _, name := range scenarioNames(t) {
		t.Run(name, func(t *testing.T) {
			s, err := status.ParseBSCC(scenario(t, name))
			if err != nil {
				t.Fatal(err)
			}
			got := strings.Join(status.Map(s).CupsLines(), "\n") + "\n"
			if want := string(readFile(t, "expected/"+name+".cups")); got != want {
				t.Errorf("golden mismatch\n got: %q\nwant: %q", got, want)
			}
		})
	}
}

// TestConformanceCases checks the independent conformance file: the XML
// document yields the BSCC text, the decoded fields, the report and the exact
// ATTR/STATE lines.
func TestConformanceCases(t *testing.T) {
	var f struct {
		Cases []struct {
			Name    string
			Origin  string
			XML     string `json:"xml"`
			BSCC    string
			Decoded struct {
				Region        string
				BST           string
				BusyState     string `json:"busy_state"`
				JobState      string `json:"job_state"`
				Cartridges    []string
				Ink           []struct{ Color, Fill, Tank string }
				Warnings      []string
				OperatorCalls []string `json:"operator_calls"`
				ServiceCall   string   `json:"service_call"`
			}
			Report struct {
				Activity string
				Levels   []int
				Names    []string
				Reasons  []string
				Message  string
			}
			Attr  string
			State []string
		}
	}
	if err := json.Unmarshal(readFile(t, "status_cases.json"), &f); err != nil {
		t.Fatal(err)
	}
	if len(f.Cases) != 15 {
		t.Fatalf("%d cases", len(f.Cases))
	}
	for _, c := range f.Cases {
		t.Run(c.Name, func(t *testing.T) {
			text, ok := status.ExtractStatus(readFile(t, c.XML))
			if !ok || text != c.BSCC {
				t.Fatalf("ExtractStatus = %q, %v", text, ok)
			}
			b, err := status.ParseBSCC(text)
			if err != nil {
				t.Fatal(err)
			}
			d := c.Decoded
			if b.HRI != d.Region || len(b.Inks) != len(d.Ink) || b.DSC != d.ServiceCall || b.DBS != d.BusyState || b.DJS != d.JobState ||
				strings.Join(b.CHD, ",") != strings.Join(d.Cartridges, ",") || strings.Join(b.DOC, ",") != strings.Join(d.OperatorCalls, ",") ||
				strings.Join(b.DWS, ",") != strings.Join(d.Warnings, ",") {
				t.Errorf("decoded fields differ: %+v vs %+v", b, d)
			}
			for i, k := range d.Ink {
				if b.Inks[i].Colour != k.Color || b.Inks[i].Fill != k.Fill || b.Inks[i].Tank != k.Tank {
					t.Errorf("ink %d: %+v vs %+v", i, b.Inks[i], k)
				}
			}
			r := status.Map(b)
			if r.FormatAttr() != c.Attr {
				t.Errorf("attr\n got %s\nwant %s", r.FormatAttr(), c.Attr)
			}
			if strings.Join(r.FormatState(), "\n") != strings.Join(c.State, "\n") {
				t.Errorf("state lines differ")
			}
			if r.Activity != c.Report.Activity || r.Message != c.Report.Message ||
				r.Markers[0].Name != c.Report.Names[0] || r.Markers[1].Name != c.Report.Names[1] ||
				r.Markers[0].Level != c.Report.Levels[0] || r.Markers[1].Level != c.Report.Levels[1] ||
				strings.Join(r.Reasons.Names(), ",") != strings.Join(c.Report.Reasons, ",") {
				t.Errorf("report differs: %+v vs %+v", r, c.Report)
			}
		})
	}
}

func TestRealSessionDocuments(t *testing.T) {
	// The requests we build equal the recorded ones.
	for name, build := range map[string]func() ([]byte, error){
		"startjob":  func() ([]byte, error) { return jobstream.StartJobXML("00000001", true) },
		"modeshift": func() ([]byte, error) { return jobstream.ModeShiftXML("00000001") },
		"endjob":    func() ([]byte, error) { return jobstream.EndJobXML("00000001") },
	} {
		got, err := build()
		if err != nil {
			t.Fatal(err)
		}
		if want := readFile(t, "session/"+name+".request.xml"); string(got) != strings.TrimRight(string(want), "\r\n") {
			t.Errorf("%s request differs from the recorded one:\n%s\n%s", name, got, want)
		}
	}
	// The real status documents are decoded.
	for _, f := range []string{"session/getstatus_idle.response.xml", "session/getstatus_processing.response.xml"} {
		text, ok := status.ExtractStatus(readFile(t, f))
		if !ok || !strings.HasPrefix(text, "BST:00;") {
			t.Errorf("%s: %q %v", f, text, ok)
		}
	}
	// The mock's documents are byte-identical to the recorded session.
	same := func(name, got string) {
		t.Helper()
		if want := string(readFile(t, name)); got != want {
			t.Errorf("%s differs from the mock's document", name)
		}
	}
	idle := scenario(t, "idle")
	same("session/getstatus_idle.response.xml", mockprinter.StatusDocument("idle", idle, ""))
	same("session/getstatus_processing.response.xml", mockprinter.StatusDocument("processing", idle, "00000001"))
	same("session/startjob.response.xml", mockprinter.ReplyDocument("StartJobResponse", "00000001"))
	same("session/modeshift.response.xml", mockprinter.ReplyDocument("VendorCmdResponse", "00000001"))
	same("session/endjob.response.xml", mockprinter.ReplyDocument("EndJobResponse", "00000001"))
}

func TestDocumentParsing(t *testing.T) {
	good := string(readFile(t, "session/getstatus_idle.response.xml"))
	bscc, _ := status.ExtractStatus([]byte(good))
	nb := "<cijn:NumberOfBytes>" + "182" + "</cijn:NumberOfBytes>"
	if !strings.Contains(good, nb) {
		t.Fatal("fixture changed")
	}
	cases := []struct {
		name, in string
		ok       bool
	}{
		{"plain", good, true},
		{"junk before", "\x00\x01junk" + good, true},
		{"two documents", good + good, true},
		{"truncated", good[:len(good)-30], false},
		{"wrong length", strings.Replace(good, nb, "<cijn:NumberOfBytes>181</cijn:NumberOfBytes>", 1), false},
		{"non-digit length", strings.Replace(good, nb, "<cijn:NumberOfBytes>18x</cijn:NumberOfBytes>", 1), false},
		{"zero length", strings.Replace(good, nb, "<cijn:NumberOfBytes>0</cijn:NumberOfBytes>", 1), false},
		{"response not OK", strings.Replace(good, "<ivec:response>OK", "<ivec:response>NG", 1), false},
		{"other operation", strings.Replace(good, "GetStatusResponse", "StartJobResponse", 1), false},
		{"cdata", strings.Replace(good, "<cijn:ReadData>"+bscc, "<cijn:ReadData><![CDATA["+bscc+"]]>", 1), true},
		{"other prefix", strings.NewReplacer("ivec:", "iv:", "xmlns:ivec", "xmlns:iv").Replace(good), true},
		{"not xml", "BST:00;", false},
		{"empty", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			text, ok := status.ExtractStatus([]byte(c.in))
			if ok != c.ok || (ok && text != bscc) {
				t.Errorf("ok=%v text=%q", ok, text)
			}
		})
	}
	// a malformed first document does not hide a valid second one
	if _, ok := status.ExtractStatus([]byte("<?xml version=\"1.0\"?><cmd><oops></cmd>" + good)); !ok {
		t.Error("valid document after a malformed one was missed")
	}
}

func FuzzParse(f *testing.F) {
	f.Add("BST:00;CHD:CLM,BKM;CTK:CL,SET,/,BK,SET;CIR:CL=100,BK=100;DOC:4,00,NO;")
	f.Fuzz(func(t *testing.T, s string) {
		if b, err := status.ParseBSCC(s); err == nil {
			r := status.Map(b)
			_ = r.FormatAttr()
		}
	})
}

func FuzzExtractStatus(f *testing.F) {
	f.Add(string(mustRead("session/getstatus_idle.response.xml")))
	f.Fuzz(func(t *testing.T, s string) { status.ExtractStatus([]byte(s)) })
}

func mustRead(rel string) []byte {
	b, err := os.ReadFile(filepath.Join(testdata, rel))
	if err != nil {
		panic(err)
	}
	return b
}

// session runs one status session against a mock.
func session(t *testing.T, h *mockprinter.Handle, d time.Duration) (*status.Result, error) {
	t.Helper()
	t.Setenv(status.EnvTransport, h.Transport)
	return status.Run("", status.Options{Deadline: d, EndGrace: time.Second})
}

func TestSessionAllScenarios(t *testing.T) {
	h := mockprinter.Start(t, scenario(t, "idle"))
	for _, name := range scenarioNames(t) {
		t.Run(name, func(t *testing.T) {
			h.Set(scenario(t, name))
			before := len(h.Requests())
			res, err := session(t, h, 5*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			if res.BSCC != scenario(t, name) {
				t.Errorf("raw text round trip: %q", res.BSCC)
			}
			if !res.Started || !res.Ended || res.JobID != "00000001" {
				t.Errorf("result %+v", res)
			}
			s, _ := status.ParseBSCC(res.BSCC)
			got := strings.Join(status.Map(s).CupsLines(), "\n") + "\n"
			if want := string(readFile(t, "expected/"+name+".cups")); got != want {
				t.Errorf("session output differs from offline decode")
			}
			reqs := h.Requests()[before:]
			if strings.Join(reqs, " ") != "StartJob ModeShift EndJob" {
				t.Errorf("requests %v", reqs)
			}
		})
	}
}

func TestSessionLiveChange(t *testing.T) {
	h := mockprinter.Start(t, scenario(t, "idle"))
	a, err := session(t, h, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	h.Set(scenario(t, "low_ink_colour"))
	b, err := session(t, h, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	ra, rb := mustMap(t, a.BSCC), mustMap(t, b.BSCC)
	if ra.Markers[1].Level != 100 || rb.Markers[1].Level != 10 {
		t.Errorf("levels %d -> %d", ra.Markers[1].Level, rb.Markers[1].Level)
	}
}

func mustMap(t *testing.T, text string) status.Report {
	t.Helper()
	s, err := status.ParseBSCC(text)
	if err != nil {
		t.Fatal(err)
	}
	return status.Map(s)
}

func TestSessionFailureInjection(t *testing.T) {
	for _, mode := range []string{"silent", "no-status", "drop-startjob"} {
		t.Run(mode, func(t *testing.T) {
			h := mockprinter.Start(t, "#"+mode+"\n"+scenario(t, "idle"))
			start := time.Now()
			res, err := session(t, h, time.Second)
			if err == nil {
				t.Fatal("query succeeded")
			}
			if d := time.Since(start); d > 5*time.Second {
				t.Errorf("not bounded: %v", d)
			}
			var se *status.SessionError
			if !asSessionError(err, &se) {
				t.Fatalf("error type %T", err)
			}
			if mode == "no-status" {
				time.Sleep(50 * time.Millisecond)
				if got := strings.Join(h.Requests(), " "); got != "StartJob ModeShift EndJob" {
					t.Errorf("EndJob is still sent after StartJob was accepted: %q (res %+v)", got, res)
				}
			}
		})
	}
	t.Run("hangup", func(t *testing.T) {
		h := mockprinter.Start(t, "#hangup\n"+scenario(t, "idle"))
		if _, err := session(t, h, 2*time.Second); err == nil {
			t.Fatal("hang-up gave success")
		}
	})
	t.Run("no mock", func(t *testing.T) {
		h := mockprinter.Start(t, scenario(t, "idle"))
		h.Close()
		_, err := session(t, h, time.Second)
		if err == nil || !strings.Contains(err.Error(), "no matching printer") {
			t.Errorf("err = %v", err)
		}
	})
}

func asSessionError(err error, target **status.SessionError) bool {
	for err != nil {
		if se, ok := err.(*status.SessionError); ok {
			*target = se
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

func TestSerialFromURI(t *testing.T) {
	for in, want := range map[string]string{
		"usb://Canon/MP280%20series?serial=ABC123":         "ABC123",
		"mp280usb://Canon/MP280%20series?serial=A%20B&x=1": "A B",
		"usb://Canon/MP280%20series?interface=1&serial=Z9": "Z9",
		"usb://Canon/MP280%20series":                       "",
		"usb://Canon/MP280%20series?serial=100%":           "100%",
	} {
		if got := status.SerialFromURI(in); got != want {
			t.Errorf("%s: %q, want %q", in, got, want)
		}
	}
}
