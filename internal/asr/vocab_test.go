package asr

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseVocab(t *testing.T) {
	in := "<unk> 0\n▁the 2\n▁a 1\n<blk> 3\n"
	got, err := parseVocab(strings.NewReader(in))
	if err != nil {
		t.Fatalf("parseVocab: %v", err)
	}
	want := []string{"<unk>", "▁a", "▁the", "<blk>"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseVocab() = %v, want %v", got, want)
	}
}

func TestParseVocab_MalformedLine(t *testing.T) {
	if _, err := parseVocab(strings.NewReader("no-space-here\n")); err == nil {
		t.Fatal("parseVocab: want error for line with no id, got nil")
	}
}

func TestDecodeTokens(t *testing.T) {
	vocab := []string{"<unk>", "▁hello", "▁world", "!"}
	got := decodeTokens([]int32{1, 2, 3}, vocab)
	want := "hello world!"
	if got != want {
		t.Errorf("decodeTokens() = %q, want %q", got, want)
	}
}

func TestDecodeTokens_SkipsOutOfRangeIDs(t *testing.T) {
	vocab := []string{"<unk>", "▁hi"}
	got := decodeTokens([]int32{1, 99, -1}, vocab)
	if got != "hi" {
		t.Errorf("decodeTokens() = %q, want %q", got, "hi")
	}
}
