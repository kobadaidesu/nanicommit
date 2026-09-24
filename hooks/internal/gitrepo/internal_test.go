package gitrepo

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestCQuote(t *testing.T) {
	for in, want := range map[string]string{
		"a/plain.txt":      "a/plain.txt",
		"a/日本語 空白.txt":     "a/日本語 空白.txt",
		"a/tab\tx":         `"a/tab\tx"`,
		"a/nl\nx":          `"a/nl\nx"`,
		`a/q"b\c`:          `"a/q\"b\\c"`,
		"a/ctl\x01\x7f":    `"a/ctl\001\177"`,
		"a/bell\a\b\v\f\r": `"a/bell\a\b\v\f\r"`,
	} {
		if got := cQuote(in); got != want {
			t.Errorf("cQuote(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestCollectorCutsAtCharacterBoundary(t *testing.T) {
	text := strings.Repeat("あ", 10) // 30 bytes
	for limit := 0; limit <= 32; limit++ {
		c := newCollector(limit)
		c.write([]byte(text[:15]))
		c.write([]byte(text[15:]))
		got, truncated, invalid := c.finish()
		if invalid || !utf8.ValidString(got) || len(got) > limit || !strings.HasPrefix(text, got) {
			t.Fatalf("limit %d: got %q invalid=%v", limit, got, invalid)
		}
		if wantLen := limit / 3 * 3; limit < 30 && (len(got) != wantLen || !truncated) {
			t.Errorf("limit %d: len %d truncated %v, want len %d truncated", limit, len(got), truncated, wantLen)
		}
		if limit >= 30 && (got != text || truncated) {
			t.Errorf("limit %d: got %q truncated %v", limit, got, truncated)
		}
	}
}

func TestCollectorInvalidUTF8(t *testing.T) {
	c := newCollector(100)
	c.write([]byte("ok \xff\xfe end"))
	got, truncated, invalid := c.finish()
	if !invalid || truncated || got != "ok � end" {
		t.Fatalf("got %q truncated=%v invalid=%v", got, truncated, invalid)
	}
	// Replacement characters are longer than the bytes they replace; the
	// result must still respect the limit.
	c = newCollector(6)
	c.write([]byte("\xffa\xffb\xffc"))
	got, truncated, _ = c.finish()
	if len(got) > 6 || !utf8.ValidString(got) || !truncated {
		t.Fatalf("got %q (%d bytes) truncated=%v", got, len(got), truncated)
	}
}

func TestParseParents(t *testing.T) {
	raw := []byte("tree 1111\nparent aaaa\nparent bbbb\nauthor A <a> 1 +0000\ncommitter A <a> 1 +0000\ngpgsig -----BEGIN-----\n parent cccc\n -----END-----\n\nmessage\nparent dddd\n")
	got, err := parseParents(raw)
	if err != nil || strings.Join(got, ",") != "aaaa,bbbb" {
		t.Fatalf("parseParents = %v, %v", got, err)
	}
	if got, err := parseParents([]byte("tree 1\nauthor x\n\nroot")); err != nil || got == nil || len(got) != 0 {
		t.Fatalf("root commit: %v, %v", got, err)
	}
}
