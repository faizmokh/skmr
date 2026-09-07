// Package terminal makes untrusted skill text safe for terminal display.
package terminal

import (
	"github.com/charmbracelet/x/ansi"
	"strings"
	"unicode"
)

func Safe(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, ansi.Strip(s))
}
