package main

import (
	"os"
	"reflect"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestParseAgainstIrcDocsCorpus runs the vendored ircdocs/parser-tests
// msg-split.yaml against our parser. Every test case is an "input: ..., atoms:
// {tags, source, verb, params}" record.
func TestParseAgainstIrcDocsCorpus(t *testing.T) {
	type atoms struct {
		Tags   map[string]string `yaml:"tags"`
		Source string            `yaml:"source"`
		Verb   string            `yaml:"verb"`
		Params []string          `yaml:"params"`
	}
	type tcase struct {
		Input string `yaml:"input"`
		Atoms atoms  `yaml:"atoms"`
	}
	type corpus struct {
		Tests []tcase `yaml:"tests"`
	}

	raw, err := os.ReadFile("parser-tests/msg-split.yaml")
	if err != nil {
		t.Fatalf("read corpus: %v", err)
	}
	var c corpus
	if err := yaml.Unmarshal(raw, &c); err != nil {
		t.Fatalf("yaml: %v", err)
	}
	if len(c.Tests) == 0 {
		t.Fatal("empty corpus")
	}
	t.Logf("running %d cases from ircdocs/parser-tests msg-split.yaml", len(c.Tests))

	for i, tc := range c.Tests {
		got := Parse(tc.Input)

		// Tags: nil map and empty map should compare equal.
		gotTags := got.Tags
		wantTags := tc.Atoms.Tags
		if len(gotTags) == 0 {
			gotTags = nil
		}
		if len(wantTags) == 0 {
			wantTags = nil
		}
		if !reflect.DeepEqual(gotTags, wantTags) {
			t.Errorf("case %d %q: tags = %#v, want %#v", i, tc.Input, gotTags, wantTags)
		}
		if got.Source != tc.Atoms.Source {
			t.Errorf("case %d %q: source = %q, want %q", i, tc.Input, got.Source, tc.Atoms.Source)
		}
		if got.Verb != tc.Atoms.Verb {
			t.Errorf("case %d %q: verb = %q, want %q", i, tc.Input, got.Verb, tc.Atoms.Verb)
		}

		gotParams := got.Params
		wantParams := tc.Atoms.Params
		if len(gotParams) == 0 {
			gotParams = nil
		}
		if len(wantParams) == 0 {
			wantParams = nil
		}
		if !reflect.DeepEqual(gotParams, wantParams) {
			t.Errorf("case %d %q: params = %#v, want %#v", i, tc.Input, gotParams, wantParams)
		}
	}
}
