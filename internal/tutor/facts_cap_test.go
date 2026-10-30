package tutor

import "testing"

// TestReviewKeepsFiveFactsDeduplicated pins the per-message cap and the folding
// of two spellings of the same key.
func TestReviewKeepsFiveFactsDeduplicated(t *testing.T) {
	out := normalizeFacts([]Fact{
		{Key: "name", Value: "fito"}, {Key: "Name", Value: "Fito"},
		{Key: "city", Value: "Madrid"}, {Key: "country", Value: "Argentina"},
		{Key: "hobby", Value: "cooking"}, {Key: "job", Value: "nurse"},
		{Key: "pet", Value: "cat"},
	})
	if len(out) != 5 {
		t.Fatalf("kept %d facts, want 5: %v", len(out), out)
	}
	if out[0].Key != "name" || out[0].Value != "Fito" {
		t.Errorf("first = %v, want name=Fito", out[0])
	}
}
