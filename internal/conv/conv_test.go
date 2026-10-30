package conv

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func dataWithTurns(n int, size int) Data {
	d := Data{Profile: Profile{Level: "B1"}}
	for i := 0; i < n; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		d.AddTurn(role, fmt.Sprintf("turn %d ", i)+strings.Repeat("x", size))
	}
	return d
}

func TestBuildAlwaysStartsWithSystemAndKeepsNewestTurn(t *testing.T) {
	d := dataWithTurns(41, 200) // ends on a user turn
	msgs := Build(d, 2048, 256)
	if msgs[0].Role != "system" || !strings.Contains(msgs[0].Content, "conversation partner") {
		t.Fatal("first message must be the tutor system prompt")
	}
	last := msgs[len(msgs)-1]
	if last.Content != d.Turns[len(d.Turns)-1].Content {
		t.Error("newest turn missing")
	}
	if msgs[1].Role != "user" {
		t.Errorf("window starts on %q, want user", msgs[1].Role)
	}
}

func TestBuildStaysWithinBudget(t *testing.T) {
	for _, numCtx := range []int{2048, 4096, 8192} {
		d := dataWithTurns(120, 300)
		total := 0
		for _, m := range Build(d, numCtx, 256) {
			total += EstimateTokens(m.Content) + 6
		}
		if total > numCtx-256 {
			t.Errorf("num_ctx %d: prompt estimate %d exceeds budget %d", numCtx, total, numCtx-256)
		}
	}
}

func TestBuildIncludesSummaryAndProfile(t *testing.T) {
	d := dataWithTurns(2, 10)
	d.Summary = "The learner is a nurse planning a trip to Dublin."
	d.Profile.Level = "B2"
	d.AddTopic("hiking")
	d.RecordMistake("past simple")
	sys := Build(d, 4096, 256)[0].Content
	for _, want := range []string{"nurse planning a trip", "B2", "hiking", "past simple"} {
		if !strings.Contains(sys, want) {
			t.Errorf("system prompt lacks %q", want)
		}
	}
}

func TestPlanFold(t *testing.T) {
	small := dataWithTurns(6, 50)
	if n := PlanFold(small, 8192); n != 0 {
		t.Errorf("small conversation folded %d turns", n)
	}
	big := dataWithTurns(60, 400)
	n := PlanFold(big, 4096)
	if n == 0 {
		t.Fatal("large conversation should fold")
	}
	if big.Turns[n].Role != "user" {
		t.Error("verbatim window must start at a user turn")
	}
	if n >= len(big.Turns)-1 {
		t.Error("must keep some recent turns verbatim")
	}
}

func TestFoldSummaryHonoursEpoch(t *testing.T) {
	d := dataWithTurns(10, 10)
	epoch := d.Epoch
	d.Reset()
	d.AddTurn("user", "fresh start")
	if d.FoldSummary("stale summary", 4, epoch) {
		t.Error("a summary planned before a reset must not be applied")
	}
	if d.Summary != "" || len(d.Turns) != 1 {
		t.Error("state changed by a stale fold")
	}
	if !d.FoldSummary("ok", 1, d.Epoch) || d.Summary != "ok" || len(d.Turns) != 0 {
		t.Error("a current fold should apply")
	}
}

func TestResetKeepsProfile(t *testing.T) {
	d := dataWithTurns(4, 10)
	d.Profile.Level = "C1"
	d.RecordMistake("articles")
	d.Reset()
	if d.Profile.Level != "C1" || d.Profile.Mistakes["articles"] != 1 {
		t.Error("profile lost on reset")
	}
	if len(d.Turns) != 0 || d.Summary != "" {
		t.Error("conversation not cleared")
	}
}

func TestMistakeAndTopicLimits(t *testing.T) {
	var d Data
	for i := 0; i < 100; i++ {
		d.RecordMistake(fmt.Sprintf("issue %d", i))
	}
	for i := 0; i < 5; i++ {
		d.RecordMistake("articles")
	}
	if len(d.Profile.Mistakes) > maxMistakes {
		t.Errorf("%d mistakes kept", len(d.Profile.Mistakes))
	}
	if top := d.TopMistakes(1); len(top) != 1 || top[0] != "articles" {
		t.Errorf("top mistake %v", top)
	}
	for i := 0; i < 10; i++ {
		d.AddTopic(fmt.Sprintf("topic %d", i))
	}
	if len(d.Profile.Topics) != maxTopics {
		t.Errorf("%d topics kept", len(d.Profile.Topics))
	}
}

func TestHardCapOnStoredTurns(t *testing.T) {
	d := dataWithTurns(maxTurnsOnDisk+50, 5)
	if len(d.Turns) != maxTurnsOnDisk {
		t.Errorf("%d turns stored", len(d.Turns))
	}
}

func TestStoreRoundTripAndAtomicWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "state.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if s.View().Profile.Level != "B1" {
		t.Error("default level")
	}
	if err := s.Update(func(d *Data) {
		d.AddTurn("user", "hello")
		d.AddTopic("music")
		d.RecordMistake("word order")
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".tmp"); err == nil {
		t.Error("temp file left behind")
	}
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	d := s2.View()
	if len(d.Turns) != 1 || d.Profile.Topics[0] != "music" || d.Profile.Mistakes["word order"] != 1 {
		t.Errorf("round trip lost data: %+v", d)
	}
	// View must be a deep copy.
	d.Turns[0].Content = "mutated"
	d.Profile.Mistakes["word order"] = 99
	if s2.View().Turns[0].Content != "hello" || s2.View().Profile.Mistakes["word order"] != 1 {
		t.Error("View leaks internal state")
	}
}

func TestOpenRejectsCorruptState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); err == nil {
		t.Error("corrupt state must be an error, not silently replaced")
	}
}
