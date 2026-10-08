package limit

import "testing"

func TestBucketRefills(t *testing.T) {
	b := NewBucket(2, 1, 0)
	if !b.Take(0) || !b.Take(0) {
		t.Fatalf("fresh bucket should hold two tokens")
	}
	if b.Take(500) {
		t.Fatalf("bucket refilled too fast")
	}
	if !b.Take(1000) {
		t.Fatalf("bucket should have refilled one token after a second")
	}
}
