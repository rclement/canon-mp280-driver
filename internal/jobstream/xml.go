// SPDX-License-Identifier: GPL-2.0-only

// Package jobstream produces everything in the MP280 printer stream except the
// raster bands: the IVEC XML wrappers, the BJL text block with the job time,
// the "ESC (" page-setup commands and the page and job footers.
//
// A print job on the wire is, in order:
//
//	IVEC StartJob XML          (optional: the USB language monitor writes it
//	IVEC ModeShift XML          instead when the job goes through CUPS)
//	SSR definition             ESC [ K ... "SSR=DF;"
//	BJL block                  "BJLSTART / ControlMode=Common / SetTime=... / BJLEND"
//	per page: page header      ESC ( a, b, q (first page only), d, t, c, p, l, u
//	          raster layer     (package raster)
//	          page footer      ESC ( e + form feed
//	job footer                 ESC ( b 0, ESC ( a 0, ESC @
//	IVEC EndJob XML            (optional, as above)
package jobstream

import (
	"fmt"
)

// DefaultJobID is the job id the original front end puts in the IVEC documents. The
// USB language monitor substitutes the id the printer returned.
const DefaultJobID = "00000001"

// jobIDLen is the fixed length of an IVEC job id.
const jobIDLen = 8

const (
	xmlHead = `<?xml version="1.0" encoding="utf-8" ?><cmd xmlns:ivec="http://www.canon.com/ns/cmd/2008/07/common/"`
	xmlVCN  = ` xmlns:vcn="http://www.canon.com/ns/cmd/2008/07/canon/"`
)

func checkJobID(id string) error {
	if len(id) != jobIDLen {
		return fmt.Errorf("jobstream: job id %q must be %d characters", id, jobIDLen)
	}
	return nil
}

// StartJobXML is the IVEC StartJob document. bidi says whether the host will
// read status back from the printer (the front end writes "0", the language
// monitor "1").
func StartJobXML(jobID string, bidi bool) ([]byte, error) {
	if err := checkJobID(jobID); err != nil {
		return nil, err
	}
	flag := "0"
	if bidi {
		flag = "1"
	}
	return []byte(xmlHead + `><ivec:contents><ivec:operation>StartJob</ivec:operation>` +
		`<ivec:param_set servicetype="print"><ivec:jobID>` + jobID + `</ivec:jobID>` +
		`<ivec:bidi>` + flag + `</ivec:bidi></ivec:param_set></ivec:contents></cmd>`), nil
}

// ModeShiftXML is the IVEC vendor command that switches the printer into its
// print mode (mode 1) after StartJob.
func ModeShiftXML(jobID string) ([]byte, error) {
	if err := checkJobID(jobID); err != nil {
		return nil, err
	}
	return []byte(xmlHead + xmlVCN + `><ivec:contents><ivec:operation>VendorCmd</ivec:operation>` +
		`<ivec:param_set servicetype="print"><ivec:jobID>` + jobID + `</ivec:jobID>` +
		`<vcn:ijoperation>ModeShift</vcn:ijoperation><vcn:ijmode>1</vcn:ijmode>` +
		`</ivec:param_set></ivec:contents></cmd>`), nil
}

// EndJobXML is the IVEC EndJob document.
func EndJobXML(jobID string) ([]byte, error) {
	if err := checkJobID(jobID); err != nil {
		return nil, err
	}
	return []byte(xmlHead + `><ivec:contents><ivec:operation>EndJob</ivec:operation>` +
		`<ivec:param_set servicetype="print"><ivec:jobID>` + jobID + `</ivec:jobID>` +
		`</ivec:param_set></ivec:contents></cmd>`), nil
}
