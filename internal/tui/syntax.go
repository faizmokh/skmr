package tui

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/faizmokh/skmr/internal/terminal"
)

var (
	keywordColor  = lipgloss.AdaptiveColor{Light: "90", Dark: "141"}
	stringColor   = lipgloss.AdaptiveColor{Light: "28", Dark: "114"}
	numberColor   = lipgloss.AdaptiveColor{Light: "130", Dark: "221"}
	functionColor = lipgloss.AdaptiveColor{Light: "25", Dark: "81"}
	operatorColor = lipgloss.AdaptiveColor{Light: "125", Dark: "212"}

	keywordSyntaxStyle  = lipgloss.NewStyle().Foreground(keywordColor).Bold(true)
	stringSyntaxStyle   = lipgloss.NewStyle().Foreground(stringColor)
	numberSyntaxStyle   = lipgloss.NewStyle().Foreground(numberColor)
	functionSyntaxStyle = lipgloss.NewStyle().Foreground(functionColor)
	operatorSyntaxStyle = lipgloss.NewStyle().Foreground(operatorColor)
	commentSyntaxStyle  = mutedStyle.Italic(true)

	lexerCache sync.Map
)

func highlightSyntax(path, source string) (result string) {
	source = terminal.Safe(source)
	defer func() {
		if recover() != nil {
			result = source
		}
	}()
	if source == "" {
		return ""
	}
	lexer := syntaxLexer(path)
	if lexer == nil {
		return source
	}
	tokens, err := chroma.Tokenise(lexer, nil, source)
	if err != nil {
		return source
	}
	result = ""
	for _, token := range tokens {
		value := terminal.Safe(token.Value)
		switch {
		case token.Type.InCategory(chroma.Keyword):
			value = keywordSyntaxStyle.Render(value)
		case token.Type.InSubCategory(chroma.LiteralString):
			value = stringSyntaxStyle.Render(value)
		case token.Type.InSubCategory(chroma.LiteralNumber):
			value = numberSyntaxStyle.Render(value)
		case token.Type.InSubCategory(chroma.NameFunction), token.Type.InSubCategory(chroma.NameClass):
			value = functionSyntaxStyle.Render(value)
		case token.Type.InCategory(chroma.Comment):
			value = commentSyntaxStyle.Render(value)
		case token.Type.InCategory(chroma.Operator):
			value = operatorSyntaxStyle.Render(value)
		}
		result += value
	}
	return result
}

func renderSideDiffCell(cell sideDiffCell, width, horizontal int) string {
	if !cell.present {
		return strings.Repeat(" ", max(0, width))
	}
	gutter := ""
	if width >= 10 {
		lineNumber := "    "
		if cell.line > 0 {
			lineNumber = fmt.Sprintf("%3d ", cell.line)
		}
		gutter = mutedStyle.Render(lineNumber)
	}
	marker, markerStyle := " ", mutedStyle
	if cell.kind == diffDeletion {
		marker, markerStyle = "-", errorStyle
	} else if cell.kind == diffAddition {
		marker, markerStyle = "+", successStyle
	}
	contentWidth := max(0, width-ansi.StringWidth(gutter)-1)
	content := panSyntax(highlightSyntax(cell.path, cell.text), horizontal, contentWidth)
	return pad(gutter+markerStyle.Render(marker)+content, width)
}

func panSyntax(source string, offset, width int) string {
	if width <= 0 {
		return ""
	}
	total := ansi.StringWidth(source)
	offset = min(max(0, offset), total)
	leftMarker := offset > 0
	rightMarker := total > offset+width
	contentWidth := width
	if leftMarker {
		contentWidth--
	}
	if rightMarker && contentWidth > 0 {
		contentWidth--
	}
	segment := ansi.Cut(source, offset, offset+max(0, contentWidth))
	if leftMarker {
		segment = mutedStyle.Render("‹") + segment
	}
	if rightMarker {
		segment += mutedStyle.Render("›")
	}
	return pad(segment, width)
}

func syntaxLexer(path string) chroma.Lexer {
	key := filepath.Base(path)
	if cached, ok := lexerCache.Load(key); ok {
		if cached == false {
			return nil
		}
		return cached.(chroma.Lexer)
	}
	lexer := lexers.Match(key)
	if lexer == nil {
		lexerCache.Store(key, false)
		return nil
	}
	lexer = chroma.Coalesce(lexer)
	lexerCache.Store(key, lexer)
	return lexer
}
