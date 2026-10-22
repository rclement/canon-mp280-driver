// SPDX-License-Identifier: GPL-2.0-only

package printdb

import (
	"encoding/binary"
	"testing"
)

// resourcePayload builds a type-2 table (first matching record) with one key
// column reading query slot 3, two data bytes per record.
func resourcePayload(records [][2]int16) []byte {
	p := make([]byte, 48)
	le := binary.LittleEndian
	le.PutUint16(p[0:], 2) // type: first matching record
	le.PutUint16(p[4:], 1) // one key column
	le.PutUint16(p[6:], 1) // elements per record
	le.PutUint16(p[8:], 2) // bytes per element
	le.PutUint16(p[10:], uint16(len(records)))
	le.PutUint16(p[16:], 3) // key column compares query slot 3
	for _, r := range records {
		p = le.AppendUint16(p, uint16(r[0]))
		p = le.AppendUint16(p, uint16(r[1]))
	}
	return p
}

func TestMatchRecords(t *testing.T) {
	p := resourcePayload([][2]int16{{5, 100}, {-0x8000, 200}})
	var q ResourceQuery
	q[3] = 5
	data, n, err := matchRecords(p, &q, 1000)
	if err != nil || n != 1 || binary.LittleEndian.Uint16(data) != 100 {
		t.Fatalf("exact key: data %v n %d err %v", data, n, err)
	}
	q[3] = 9 // only the wildcard record matches
	data, n, err = matchRecords(p, &q, 1000)
	if err != nil || n != 1 || binary.LittleEndian.Uint16(data) != 200 {
		t.Fatalf("wildcard: data %v n %d err %v", data, n, err)
	}
	if _, _, err := matchRecords(p[:50], &q, 1000); err == nil {
		t.Error("truncated table accepted")
	}
}

func TestInt16sInt32s(t *testing.T) {
	if got := Int16s([]byte{0xff, 0xff, 0x34, 0x12}); got[0] != -1 || got[1] != 0x1234 {
		t.Errorf("Int16s = %v", got)
	}
	if got := Int32s([]byte{0, 0, 0, 0x80}); got[0] != -0x80000000 {
		t.Errorf("Int32s = %v", got)
	}
}
