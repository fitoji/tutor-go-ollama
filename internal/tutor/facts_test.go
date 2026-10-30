package tutor

import "testing"

func TestNameFromText(t *testing.T) {
	cases := map[string]string{
		"my name is Fito":        "Fito",
		"My name is Ana.":        "Ana",
		"Hi! My name is José":    "José",
		"me llamo Fito":          "Fito",
		"call me Bob":            "Bob",
		"my name is not a topic": "",
		"I'm tired":              "",
		"":                       "",
	}
	for in, want := range cases {
		if got := nameFromText(in); got != want {
			t.Errorf("nameFromText(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTitleCaseForNames(t *testing.T) {
	cases := map[string]string{
		"fito":      "Fito",
		"ana maría": "Ana María",
		"ana-maría": "Ana-María",
		"McDonald":  "McDonald",
		"ÁLVARO":    "ÁLVARO",
		"jean luc":  "Jean Luc",
		"o'brien":   "O'brien",
	}
	for in, want := range cases {
		if got := titleCase(in); got != want {
			t.Errorf("titleCase(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestNormalizeFactRejectsNoise pins what a lazy model must not be able to
// store: template words, placeholders and empty halves.
func TestNormalizeFactRejectsNoise(t *testing.T) {
	good := []Fact{
		{Key: "name", Value: "Fito"},
		{Key: "city", Value: "Madrid"},
		{Key: "hobby", Value: "cooking"},
		{Key: "job", Value: "nurse"},
		// A multi-word key is legitimate; normalizeFacts folds it to a dash.
		{Key: "home city", Value: "Madrid"},
	}
	for _, f := range good {
		if _, ok := normalizeFact(f); !ok {
			t.Errorf("%v was rejected", f)
		}
	}
	bad := []Fact{
		{Key: "key", Value: "value"},
		{Key: "name", Value: "your_name"},
		{Key: "name", Value: "name"},
		{Key: "city", Value: "<city>"},
		{Key: "", Value: "x"},
		{Key: "city", Value: ""},
	}
	for _, f := range bad {
		if _, ok := normalizeFact(f); ok {
			t.Errorf("%v was accepted", f)
		}
	}
	if f, _ := normalizeFact(Fact{Key: "name", Value: `"fito"`}); f.Value != "Fito" {
		t.Errorf("quotes should be trimmed and the name capitalised, got %q", f.Value)
	}
}
