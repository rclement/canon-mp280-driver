// SPDX-License-Identifier: GPL-2.0-only

package status

import (
	"bytes"
	"encoding/xml"
	"strconv"
	"strings"
)

// Operation names found in <ivec:operation> of the printer's answers.
const (
	opStartJobResponse  = "StartJobResponse"
	opVendorCmdResponse = "VendorCmdResponse"
	opEndJobResponse    = "EndJobResponse"
	opGetStatusResponse = "GetStatusResponse"
	ijModeShiftResponse = "ModeShiftResponse"
	responseOK          = "OK"
	documentEnd         = "</cmd>"
	documentStart       = "<?xml"
	jobIDLen            = 8
	maxStatusTextBytes  = 2047
	maxBufferedBytes    = 64 << 10
)

// document is the part of an IVEC reply this package looks at. Elements are
// matched by local name, so the namespace prefixes the printer chooses do not
// matter. Only direct children of <param_set> count: while a job is open the
// status documents also carry a nested <jobstatus><jobID>.
type document struct {
	XMLName   xml.Name `xml:"cmd"`
	Operation string   `xml:"contents>operation"`
	Params    struct {
		ServiceType string `xml:"servicetype,attr"`
		Response    string `xml:"response"`
		JobID       string `xml:"jobID"`
		IJOperation string `xml:"ijoperation"`
		IJResponse  string `xml:"ijresponse"`
		Status      struct {
			NumberOfBytes string `xml:"NumberOfBytes"`
			ReadData      string `xml:"ReadData"`
		} `xml:"IJPrinterStatus"`
	} `xml:"contents>param_set"`
}

// splitDocuments cuts the buffered input into complete IVEC documents (each
// ends with "</cmd>"; text before "<?xml" is junk and skipped). rest is the
// incomplete tail that more reads may complete.
func splitDocuments(buf []byte) (docs [][]byte, rest []byte) {
	for {
		start := bytes.Index(buf, []byte(documentStart))
		if start < 0 {
			// Keep a possible partial "<?xm" at the end.
			if i := bytes.LastIndexByte(buf, '<'); i >= 0 && len(buf)-i < len(documentStart) {
				return docs, buf[i:]
			}
			return docs, nil
		}
		end := bytes.Index(buf[start:], []byte(documentEnd))
		if end < 0 {
			return docs, buf[start:]
		}
		end += start + len(documentEnd)
		docs = append(docs, buf[start:end])
		buf = buf[end:]
	}
}

// decode parses one complete document; ok is false for anything that is not a
// well-formed <cmd> document.
func decode(raw []byte) (d document, ok bool) {
	if err := xml.NewDecoder(bytes.NewReader(raw)).Decode(&d); err != nil {
		return d, false
	}
	return d, true
}

// replyKind selects which answer matchReply waits for.
type replyKind int

const (
	replyStartJob replyKind = iota
	replyModeShift
	replyEndJob
)

// matchReply scans the buffered documents for the answer to a session
// request. The answer must say response OK; for ModeShift ijoperation
// ModeShiftResponse with ijresponse OK; and (except for StartJob, whose
// answer defines the id) carry the expected 8-character job id. It returns
// the job id of the answer.
func matchReply(buf []byte, kind replyKind, jobID string) (id string, found bool) {
	docs, _ := splitDocuments(buf)
	for _, raw := range docs {
		d, ok := decode(raw)
		if !ok {
			continue
		}
		p := d.Params
		want := map[replyKind]string{replyStartJob: opStartJobResponse, replyModeShift: opVendorCmdResponse, replyEndJob: opEndJobResponse}[kind]
		if strings.TrimSpace(d.Operation) != want || strings.TrimSpace(p.Response) != responseOK {
			continue
		}
		got := strings.TrimSpace(p.JobID)
		if got == "" {
			continue
		}
		if kind == replyStartJob {
			if len(got) != jobIDLen {
				continue
			}
			return got, true
		}
		if len(got) == jobIDLen && got != jobID {
			continue
		}
		if kind == replyModeShift && (strings.TrimSpace(p.IJOperation) != ijModeShiftResponse || strings.TrimSpace(p.IJResponse) != responseOK) {
			continue
		}
		return got, true
	}
	return "", false
}

// ExtractStatus returns the BSCC text of the first valid GetStatusResponse in
// buf. A response is valid when the operation is GetStatusResponse, the
// response is OK, NumberOfBytes is all digits, non-zero and equal to the
// length of ReadData (CDATA or plain text), and the text is shorter than
// 2048 bytes.
func ExtractStatus(buf []byte) (text string, ok bool) {
	docs, _ := splitDocuments(buf)
	for _, raw := range docs {
		d, good := decode(raw)
		if !good {
			continue
		}
		p := d.Params
		if strings.TrimSpace(d.Operation) != opGetStatusResponse || strings.TrimSpace(p.Response) != responseOK {
			continue
		}
		nb := strings.TrimSpace(p.Status.NumberOfBytes)
		data := strings.TrimSpace(p.Status.ReadData)
		n, err := strconv.Atoi(nb)
		if err != nil || nb == "" || strings.Trim(nb, "0123456789") != "" || n <= 0 || n > maxStatusTextBytes || len(data) != n {
			continue
		}
		return data, true
	}
	return "", false
}

// dropSeen discards everything up to and including the last complete
// document: the printer streams GetStatusResponse documents continuously
// (hundreds per session), so documents already examined and not awaited are
// useless.
func dropSeen(buf []byte) []byte {
	last := bytes.LastIndex(buf, []byte(documentEnd))
	if last < 0 {
		return buf
	}
	return append(buf[:0], buf[last+len(documentEnd):]...)
}
