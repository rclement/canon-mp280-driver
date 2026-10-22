// SPDX-License-Identifier: GPL-2.0-only

// Package mockprinter is a software simulator of an MP280 on its USB bulk
// endpoints, for tests: no hardware and no USB involved. A client (the
// status session, via MP280_STATUS_TRANSPORT=unix:SOCKET) plays the host.
//
// It speaks the IVEC session the REAL printer was observed to speak (document
// formats byte-identical to the recorded session, see testdata/session):
//   - it streams GetStatusResponse documents continuously (every Interval),
//     status element "idle" before StartJob and "processing" after it;
//   - StartJob -> StartJobResponse (echoes the jobID of the request),
//     VendorCmd/ModeShift -> VendorCmdResponse, EndJob -> EndJobResponse.
//
// The BSCC text of the status documents is read from the scenario file every
// time a document is generated, so a test can change the printer's state
// while it runs. Scenario file: optional "#directive" lines, then one line of
// BSCC text. Directives (failure injection, all SYNTHETIC): #silent (never
// answers), #no-status (answers commands but sends no status documents),
// #drop-startjob (ignores StartJob), #hangup (closes the connection when
// StartJob arrives). Lines starting with "# " are comments.
package mockprinter

import (
	"bufio"
	"bytes"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"time"
)

// Scenario is the parsed scenario file.
type Scenario struct {
	Text                                string
	Silent, NoStatus, DropStart, HangUp bool
}

// LoadScenario reads a scenario file; a missing file is an empty scenario.
func LoadScenario(path string) Scenario {
	var s Scenario
	f, err := os.Open(path)
	if err != nil {
		return s
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 4096), 1<<20)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r\n")
		if strings.HasPrefix(line, "#") {
			switch line {
			case "#silent":
				s.Silent = true
			case "#no-status":
				s.NoStatus = true
			case "#drop-startjob":
				s.DropStart = true
			case "#hangup":
				s.HangUp = true
			}
			continue
		}
		if line == "" {
			continue
		}
		s.Text = line
		break
	}
	return s
}

const xmlHeader = "<?xml version=\"1.0\" encoding=\"utf-8\" ?>\n"

// toCRLF mimics the printer, which ends every line with CR LF except after
// the last </cmd>.
func toCRLF(s string) string {
	if s == "" {
		return s
	}
	body, last := s[:len(s)-1], s[len(s)-1:]
	var b strings.Builder
	for i := 0; i < len(body); i++ {
		if body[i] == '\n' && (i == 0 || body[i-1] != '\r') {
			b.WriteByte('\r')
		}
		b.WriteByte(body[i])
	}
	return b.String() + last
}

// StatusDocument builds one GetStatusResponse as the printer sends it. state
// is "idle" or "processing"; job, if non-empty, adds the job status block the
// real printer shows while a job is open.
func StatusDocument(state, bscc, job string) string {
	jobBlock := ""
	if job != "" {
		jobBlock = "\t\t\t<ivec:jobstatus>\n\t\t\t\t<ivec:jobID>" + job + "</ivec:jobID>\n\t\t\t\t<ivec:jobprogress>processing</ivec:jobprogress>\n\t\t\t</ivec:jobstatus>\n"
	}
	return toCRLF(xmlHeader + "<cmd xmlns:ivec=\"http://www.canon.com/ns/cmd/2008/07/common/\"\n" +
		"xmlns:cijn=\"http://www.canon.com/ns/wdp/2008/01/print/\">\n" +
		"\t<ivec:contents>\n\t\t<ivec:operation>GetStatusResponse</ivec:operation>\n\t\t<ivec:param_set servicetype=\"print\">\n" +
		"\t\t\t<ivec:response>OK</ivec:response>\n\t\t\t<ivec:response_detail/>\n\t\t\t<ivec:status>" + state + "</ivec:status>\n" +
		"\t\t\t<ivec:status_detail/>\n" + jobBlock + "\t\t\t<cijn:IJPrinterStatus>\n\t\t\t\t<cijn:Name>BSCCe</cijn:Name>\n" +
		"\t\t\t\t<cijn:ReadData>" + bscc + "</cijn:ReadData>\n\t\t\t\t<cijn:NumberOfBytes>" + fmt.Sprint(len(bscc)) + "</cijn:NumberOfBytes>\n" +
		"\t\t\t</cijn:IJPrinterStatus>\n\t\t</ivec:param_set>\n\t</ivec:contents>\n</cmd>\n")
}

// ReplyDocument builds StartJobResponse, VendorCmdResponse (ModeShift) or
// EndJobResponse.
func ReplyDocument(op, job string) string {
	if op == "VendorCmdResponse" {
		return toCRLF(xmlHeader + "<cmd xmlns:ivec=\"http://www.canon.com/ns/cmd/2008/07/common/\"\nxmlns:vcn=\"http://www.canon.com/ns/cmd/2008/07/canon/\">\n" +
			"\t<ivec:contents>\n\t\t<ivec:operation>" + op + "</ivec:operation>\n\t\t<ivec:param_set servicetype=\"print\">\n" +
			"\t\t\t<ivec:response>OK</ivec:response>\n\t\t\t<ivec:response_detail/>\n\t\t\t<ivec:jobID>" + job + "</ivec:jobID>\n" +
			"\t\t\t<vcn:ijoperation>ModeShiftResponse</vcn:ijoperation>\n\t\t\t\t<vcn:ijmode>1</vcn:ijmode>\n" +
			"\t\t\t<vcn:ijresponse>OK</vcn:ijresponse>\n\t\t\t<vcn:ijresponse_detail/>\n\t\t</ivec:param_set>\n\t</ivec:contents>\n</cmd>\n")
	}
	return toCRLF(xmlHeader + "<cmd xmlns:ivec=\"http://www.canon.com/ns/cmd/2008/07/common/\">\n" +
		"\t<ivec:contents>\n\t\t<ivec:operation>" + op + "</ivec:operation>\n\t\t<ivec:param_set servicetype=\"print\">\n" +
		"\t\t\t<ivec:response>OK</ivec:response>\n\t\t\t<ivec:response_detail/>\n\t\t\t<ivec:jobID>" + job + "</ivec:jobID>\n" +
		"\t\t</ivec:param_set>\n\t</ivec:contents>\n</cmd>\n")
}

// Server serves connections on a listener.
type Server struct {
	// ScenarioPath is the scenario file, re-read for every document.
	ScenarioPath string
	// Interval between status documents (default 20 ms).
	Interval time.Duration
	// Log, if set, receives one line per event: CONNECT, REQ StartJob job=ID,
	// REQ ModeShift ..., REQ EndJob ..., DISCONNECT. Calls are serialised.
	Log func(string)

	mu sync.Mutex
}

func (s *Server) logf(format string, a ...any) {
	if s.Log == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Log(fmt.Sprintf(format, a...))
}

// Serve accepts connections one at a time until the listener is closed.
func (s *Server) Serve(l net.Listener) error {
	for {
		c, err := l.Accept()
		if err != nil {
			return err
		}
		s.serve(c)
	}
}

func between(doc, open, close string) string {
	i := strings.Index(doc, open)
	if i < 0 {
		return ""
	}
	i += len(open)
	j := strings.Index(doc[i:], close)
	if j < 0 {
		return ""
	}
	return doc[i : i+j]
}

func (s *Server) serve(c net.Conn) {
	defer func() {
		s.logf("DISCONNECT")
		c.Close()
	}()
	s.logf("CONNECT")
	interval := s.Interval
	if interval <= 0 {
		interval = 20 * time.Millisecond
	}
	// Requests arrive through a reader goroutine; the main loop also emits the
	// continuous status stream.
	type chunk struct {
		b   []byte
		err error
	}
	in := make(chan chunk, 16)
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := c.Read(buf)
			if n > 0 {
				in <- chunk{b: append([]byte(nil), buf[:n]...)}
			}
			if err != nil {
				in <- chunk{err: err}
				return
			}
		}
	}()
	write := func(doc string) bool {
		_ = c.SetWriteDeadline(time.Now().Add(time.Second))
		_, err := c.Write([]byte(doc))
		return err == nil
	}
	var pending []byte
	inJob := false
	job := "00000000"
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		select {
		case ch := <-in:
			if ch.err != nil {
				return
			}
			pending = append(pending, ch.b...)
			sc := LoadScenario(s.ScenarioPath)
			for {
				end := bytes.Index(pending, []byte("</cmd>"))
				if end < 0 {
					break
				}
				doc := string(pending[:end+6])
				pending = pending[end+6:]
				reqJob := between(doc, "<ivec:jobID>", "<")
				op := between(doc, "<ivec:operation>", "<")
				switch {
				case sc.Silent:
				case op == "StartJob":
					s.logf("REQ StartJob job=%s", reqJob)
					if sc.HangUp {
						s.logf("HANGUP")
						return
					}
					if !sc.DropStart {
						job = reqJob
						if job == "" {
							job = "00000001"
						}
						inJob = true
						if !write(ReplyDocument("StartJobResponse", job)) {
							return
						}
					}
				case op == "VendorCmd":
					s.logf("REQ ModeShift job=%s", reqJob)
					if !write(ReplyDocument("VendorCmdResponse", reqJob)) {
						return
					}
				case op == "EndJob":
					s.logf("REQ EndJob job=%s", reqJob)
					inJob = false
					if !write(ReplyDocument("EndJobResponse", reqJob)) {
						return
					}
				default:
					s.logf("REQ unknown")
				}
			}
			if len(pending) > 8192 {
				pending = nil
			}
		case <-tick.C:
			sc := LoadScenario(s.ScenarioPath)
			if sc.Silent || sc.NoStatus || sc.Text == "" {
				continue
			}
			state, jid := "idle", ""
			if inJob {
				state, jid = "processing", job
			}
			if !write(StatusDocument(state, sc.Text, jid)) {
				return
			}
		}
	}
}
