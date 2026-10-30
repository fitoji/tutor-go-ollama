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

func TestSetFactNormalizesAndReportsChange(t *testing.T) {
	d := Data{Profile: Profile{Level: "B1"}}
	if !d.SetFact(" Name ", " Fito ") {
		t.Fatal("the first fact must report a change")
	}
	if got := d.Profile.Facts["name"]; got != "Fito" {
		t.Errorf("facts[name] = %q, want Fito", got)
	}
	if d.SetFact("name", "Fito") {
		t.Error("writing the same value again is not news")
	}
	if !d.SetFact("home city", "Madrid") {
		t.Fatal("a multi-word key must be accepted")
	}
	if _, ok := d.Profile.Facts["home-city"]; !ok {
		t.Errorf("multi-word keys collapse to dashes: %v", d.Profile.Facts)
	}
	if d.SetFact("name", "Fito Alvarez") {
		if d.Profile.Facts["name"] != "Fito Alvarez" {
			t.Error("a corrected fact must replace the old one")
		}
	}
	for _, c := range []struct{ key, value string }{
		{"", "x"}, {"  ", "x"}, {"city", ""}, {"city", "   "},
	} {
		if d.SetFact(c.key, c.value) {
			t.Errorf("SetFact(%q, %q) must be rejected", c.key, c.value)
		}
	}
	// A key that would not fit the budget is refused, not silently truncated
	// into a different fact.
	if d.SetFact(strings.Repeat("k", 60), "x") {
		t.Error("an oversized key must be rejected")
	}
	// A long value is cut, so a rambling model cannot blow up the state file.
	d.SetFact("job", strings.Repeat("a", 400))
	if n := len([]rune(d.Profile.Facts["job"])); n != 200 {
		t.Errorf("value length = %d, want 200", n)
	}
}

func TestFactLimitAndDelete(t *testing.T) {
	d := Data{Profile: Profile{Level: "B1"}}
	for i := 0; i < 40; i++ {
		if !d.SetFact(fmt.Sprintf("k%d", i), "v") {
			t.Fatalf("fact %d rejected below the cap", i)
		}
	}
	if d.SetFact("one-more", "v") {
		t.Error("the cap must hold")
	}
	// An existing key can still be updated at the cap.
	if !d.SetFact("k0", "other") {
		t.Error("updating an existing fact must work at the cap")
	}
	if !d.DeleteFact("K0") {
		t.Error("delete must be case-insensitive like set")
	}
	if d.DeleteFact("k0") {
		t.Error("deleting a missing fact is not a change")
	}
}

func TestMissingBasics(t *testing.T) {
	d := Data{Profile: Profile{Level: "B1"}}
	if got := d.MissingBasics(); strings.Join(got, ",") != "name,country,city,hobby" {
		t.Errorf("MissingBasics = %v", got)
	}
	d.SetFact("name", "Fito")
	d.SetFact("city", "Madrid")
	if got := d.MissingBasics(); strings.Join(got, ",") != "country,hobby" {
		t.Errorf("MissingBasics = %v", got)
	}
}

func TestSystemCarriesWhatTheTutorKnows(t *testing.T) {
	d := Data{Profile: Profile{Level: "B1"}}
	d.SetFact("name", "Fito")
	d.SetFact("city", "Madrid")

	sys := System(d)
	for _, want := range []string{"name: Fito", "city: Madrid", "country", "hobby"} {
		if !strings.Contains(sys, want) {
			t.Errorf("system prompt should mention %q:\n%s", want, sys)
		}
	}
	// With nothing known the prompt must still invite the onboarding.
	empty := System(Data{Profile: Profile{Level: "B1"}})
	if !strings.Contains(empty, "still do not know") || !strings.Contains(empty, "name") {
		t.Errorf("an empty profile should ask who the learner is:\n%s", empty)
	}
	// Once the four basics are known, the onboarding instructions go away and
	// only the "remember new facts" rule stays.
	for _, kv := range [][2]string{{"name", "Fito"}, {"country", "Argentina"}, {"city", "Madrid"}, {"hobby", "cooking"}} {
		d.SetFact(kv[0], kv[1])
	}
	full := System(d)
	if strings.Contains(full, "still do not know") {
		t.Errorf("a complete profile must not be sent back to onboarding:\n%s", full)
	}
	if !strings.Contains(full, "hobby: cooking") {
		t.Errorf("the known facts must stay in the prompt:\n%s", full)
	}
}

func TestViewCopiesFacts(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Update(func(d *Data) { d.SetFact("name", "Fito") }); err != nil {
		t.Fatal(err)
	}
	v := s.View()
	v.Profile.Facts["name"] = "mutated"
	if s.View().Profile.Facts["name"] != "Fito" {
		t.Error("View leaks the facts map")
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
