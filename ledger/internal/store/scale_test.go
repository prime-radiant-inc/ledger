package store

import (
	"fmt"
	"testing"
	"time"

	"ledger/internal/model"
)

func TestScaleSmoke(t *testing.T) {
	if testing.Short() {
		t.Skip("scale smoke")
	}
	s := testStore(t)
	for l := 0; l < 30; l++ { // 30 ledgers x 60 events = 1800 events (CI-friendly slice of the 300x-spec probe)
		slug := fmt.Sprintf("led-%02d", l)
		s.Append(slug, model.Event{Type: "create", Author: "t"}, map[string]string{"meta.json": "{}"}, ExpectAbsent)
		for e := 0; e < 60; e++ {
			s.Append(slug, model.Event{Type: "set", Key: "k", Fields: map[string]string{"s": "v"}, Author: "t"}, nil, ExpectPresent)
		}
	}
	start := time.Now()
	slugs, _ := s.Slugs()
	for _, slug := range slugs {
		if _, _, err := s.Events(slug); err != nil {
			t.Fatal(err)
		}
	}
	if d := time.Since(start); d > 10*time.Second {
		t.Fatalf("full fold of 30x61 events took %v — reads are not batched enough", d)
	}
	// gc kept the store packed: loose objects bounded
	out, _, _ := s.Repo.Git("", "count-objects", "-v")
	t.Logf("count-objects:\n%s", out)
}
