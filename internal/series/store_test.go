package series

import (
	"testing"
	"time"
)

func TestStoreExpiresEntries(t *testing.T) {
	s := NewStore(time.Second, 2)
	defer s.Close()
	now := time.Now()
	s.Touch("a", now)
	if !s.Exists("a", now.Add(500*time.Millisecond)) {
		t.Fatal("expected entry to exist")
	}
	if s.Exists("a", now.Add(2*time.Second)) {
		t.Fatal("expected entry to expire")
	}
}
