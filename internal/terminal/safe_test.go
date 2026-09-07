package terminal

import (
	"strings"
	"testing"
	"unicode"
)

func TestSafe(t *testing.T) {
	for _, input := range []string{"\x1b[31mred\x1b[0m", "hello\x1b]52;c;YXR0YWNr\a world", "\x1b]8;;https://example.com\x1b\\link\x1b]8;;\x1b\\", "hello\x00\x07\x1b\rworld", "a\u202eb\u009bc"} {
		got := Safe(input)
		for _, r := range got {
			if unicode.IsControl(r) && r != '\n' && r != '\t' || unicode.Is(unicode.Cf, r) {
				t.Fatalf("unsafe character in %q", got)
			}
		}
		if strings.Contains(got, "YXR0YWNr") {
			t.Fatal("OSC payload leaked")
		}
	}
	if got := Safe("line\n\ttext"); got != "line\n\ttext" {
		t.Fatal("formatting lost")
	}
}
