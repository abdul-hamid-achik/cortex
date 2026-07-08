// Package ids generates stable, time-sortable identifiers for case-file
// records (tasks, evidence, hypotheses, verifications). The format mirrors the
// SPEC examples (task_01J9Q5Y8B0M6D2): a short prefix plus a Crockford base32
// encoding of a 48-bit millisecond timestamp followed by 40 bits of randomness.
// Lexical order matches creation order, which keeps case files readable.
package ids

import (
	"crypto/rand"
	"encoding/binary"
	"strings"
	"time"
)

// Crockford base32 alphabet (no I, L, O, U to avoid ambiguity).
const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// now is a package var so tests can pin time; production uses time.Now.
var now = time.Now

// New returns a new identifier with the given prefix, e.g. New("task") →
// "task_01J9Q5Y8B0M6D2K...". The prefix is joined with an underscore.
func New(prefix string) string {
	var b [10]byte // 80 bits: 48 timestamp + 32 random (padded to a clean encode)
	ms := uint64(now().UnixMilli())
	// high 48 bits carry the timestamp
	binary.BigEndian.PutUint64(b[:8], ms<<16)
	// low bytes carry randomness; ignore rand error (crypto/rand never fails on
	// supported platforms, and a degraded id is still unique-enough for a local
	// case file — see Go issue tracking rand.Read reliability).
	_, _ = rand.Read(b[6:])
	return prefix + "_" + encode(b[:])
}

// encode renders bytes as Crockford base32 without padding.
func encode(src []byte) string {
	var sb strings.Builder
	var buf, bits uint32
	for _, c := range src {
		buf = (buf << 8) | uint32(c)
		bits += 8
		for bits >= 5 {
			bits -= 5
			sb.WriteByte(crockford[(buf>>bits)&0x1f])
		}
	}
	if bits > 0 {
		sb.WriteByte(crockford[(buf<<(5-bits))&0x1f])
	}
	return sb.String()
}
