package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/faizmokh/skmr/internal/skills"
	"github.com/muesli/termenv"
)

func TestBuildCopyDiffHighlightsPackageChanges(t *testing.T) {
	source, target := diffFixture(t)
	writeDiffFile(t, source, "SKILL.md", "---\nname: alpha\n---\nold value\nshared\n")
	writeDiffFile(t, target, "SKILL.md", "---\nname: alpha\n---\nnew value\nshared\n")
	writeDiffFile(t, target, "notes.md", "new file\n")
	if err := os.Symlink("old-target", filepath.Join(source, "current")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("new-target", filepath.Join(target, "current")); err != nil {
		t.Fatal(err)
	}

	diff, err := buildCopyDiff(source, target)
	if err != nil {
		t.Fatal(err)
	}
	text := diffText(diff.lines)
	for _, want := range []string{"--- selected/SKILL.md", "+++ comparison/SKILL.md", "-old value", "+new value", "+++ comparison/notes.md", "+new file", "-symlink to old-target", "+symlink to new-target", "@@"} {
		if !strings.Contains(text, want) {
			t.Fatalf("diff missing %q:\n%s", want, text)
		}
	}
	if !hasDiffKind(diff.lines, diffAddition) || !hasDiffKind(diff.lines, diffDeletion) || !hasDiffKind(diff.lines, diffHunk) {
		t.Fatal("diff lines lost semantic highlighting kinds")
	}
}

func TestBuildCopyDiffBoundsBinaryAndLargeFiles(t *testing.T) {
	source, target := diffFixture(t)
	writeDiffBytes(t, source, "binary.dat", []byte{0, 1, 2})
	writeDiffBytes(t, target, "binary.dat", []byte{0, 1, 3})
	writeDiffBytes(t, source, "large.txt", []byte(strings.Repeat("a", maxInlineDiffBytes+1)))
	writeDiffBytes(t, target, "large.txt", []byte(strings.Repeat("b", maxInlineDiffBytes+1)))
	writeDiffFile(t, source, "long-line.txt", strings.Repeat("a", maxInlineLineBytes+1))
	writeDiffFile(t, target, "long-line.txt", strings.Repeat("b", maxInlineLineBytes+1))

	diff, err := buildCopyDiff(source, target)
	if err != nil {
		t.Fatal(err)
	}
	text := diffText(diff.lines)
	if !strings.Contains(text, "Binary file differs") || !strings.Contains(text, "Large file differs") || !strings.Contains(text, "very long lines") {
		t.Fatalf("bounded fallbacks missing:\n%s", text)
	}
}

func TestDiffUsesFilenameAwareSyntaxHighlighting(t *testing.T) {
	profile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	t.Cleanup(func() { lipgloss.SetColorProfile(profile) })
	source := "package main // comment"
	highlighted := highlightSyntax("main.go", source)
	if syntaxLexer("main.go") == nil {
		t.Fatal("Go lexer was not detected from the filename")
	}
	if ansi.Strip(highlighted) != source {
		t.Fatalf("highlighting changed source text: %q", ansi.Strip(highlighted))
	}
	if highlighted == source {
		t.Fatal("Go source did not receive syntax styling")
	}
	if got := highlightSyntax("unknown.no-such-language", source); got != source {
		t.Fatalf("unknown syntax did not fall back to plain text: %q", got)
	}
}

func TestAgentConfigDiffAccess(t *testing.T) {
	m := model(t)
	source, target := diffFixture(t)
	writeDiffFile(t, source, "agents/openai.yaml", "interface: {}\n")
	writeDiffFile(t, target, "agents/openai.yaml", "policy: {}\n")
	m.result.Skills = []skills.Skill{
		{ID: "source", Name: "alpha", Path: source, ConflictID: "copies", ConflictKind: "agent_config", ConflictCount: 2},
		{ID: "target", Name: "alpha", Path: target, ConflictID: "copies", ConflictKind: "agent_config", ConflictCount: 2},
	}
	m.restoreSelection("skill:source")
	m.width, m.height = 100, 30
	next, cmd := actionKey(m, "v")
	if cmd == nil || next.pane != paneDiff {
		t.Fatal("agent config comparison unavailable")
	}
	updated, _ := next.Update(cmd())
	view := ansi.Strip(updated.(Model).View())
	if !strings.Contains(view, "openai.yaml") || !strings.Contains(view, "policy") {
		t.Fatal(view)
	}
}

func TestDiffPaneNavigationAndSanitization(t *testing.T) {
	m := model(t)
	source, target := diffFixture(t)
	writeDiffFile(t, source, "SKILL.md", "old\x1b]52;c;BADPAYLOAD\a\n")
	writeDiffFile(t, target, "SKILL.md", "new\n")
	m.result.Skills = []skills.Skill{
		{ID: "source", Name: "alpha", Path: source, ConflictID: "copies", ConflictKind: "divergent", ConflictCount: 2},
		{ID: "target", Name: "alpha", Path: target, ConflictID: "copies", ConflictKind: "divergent", ConflictCount: 2},
	}
	m.restoreSelection("skill:source")
	m.width, m.height = 60, 18

	next, cmd := actionKey(m, "v")
	m = next
	if m.pane != paneDiff || !m.diffLoading || cmd == nil || m.notice.level != noticeProgress {
		t.Fatal("v did not open the diff reader")
	}
	nextModel, _ := m.Update(cmd())
	m = nextModel.(Model)
	view := m.View()
	plain := ansi.Strip(view)
	if m.diffLoading || !strings.Contains(plain, "Differences") || !strings.Contains(plain, "-old") || !strings.Contains(plain, "+new") {
		t.Fatalf("diff reader did not render changes:\n%s", plain)
	}
	aligned := false
	for _, line := range strings.Split(plain, "\n") {
		if strings.Contains(line, "-old") && strings.Contains(line, "+new") && strings.Contains(line, "│") {
			aligned = true
			break
		}
	}
	if !aligned || !strings.Contains(plain, "SELECTED") || !strings.Contains(plain, "COMPARISON") {
		t.Fatalf("changes were not aligned side by side:\n%s", plain)
	}
	if strings.Contains(view, "BADPAYLOAD") {
		t.Fatal("diff reader rendered unsafe terminal content")
	}
	for _, size := range [][2]int{{30, 10}, {60, 18}, {80, 24}} {
		m.width, m.height = size[0], size[1]
		view = m.View()
		for _, line := range strings.Split(view, "\n") {
			if ansi.StringWidth(line) > size[0] {
				t.Fatalf("diff overflow at %v: %q", size, line)
			}
		}
		if len(strings.Split(view, "\n")) > size[1] {
			t.Fatalf("diff height overflow at %v", size)
		}
	}
	m, _ = key(m, "esc")
	if m.pane != paneList {
		t.Fatal("Esc did not return from the diff reader")
	}

	m, _ = key(m, "x")
	if m.pane != paneActions || !strings.Contains(ansi.Strip(m.View()), "View differences") {
		t.Fatal("action picker omitted View differences")
	}
}

func TestSideBySideRowsAlignReplacementsAndInsertions(t *testing.T) {
	lines := []diffLine{
		{kind: diffHeader, text: "--- selected/main.go", path: "main.go"},
		{kind: diffHeader, text: "+++ comparison/main.go", path: "main.go"},
		{kind: diffHunk, text: "@@ -1,2 +1,3 @@", path: "main.go"},
		{kind: diffDeletion, text: "-old", path: "main.go", oldLine: 1},
		{kind: diffAddition, text: "+new", path: "main.go", newLine: 1},
		{kind: diffContext, text: " shared", path: "main.go", oldLine: 2, newLine: 2},
		{kind: diffAddition, text: "+extra", path: "main.go", newLine: 3},
	}
	rows := sideBySideRows(lines)
	if len(rows) != 5 {
		t.Fatalf("got %d rows: %+v", len(rows), rows)
	}
	if rows[2].left.text != "old" || rows[2].right.text != "new" || rows[2].left.line != 1 || rows[2].right.line != 1 {
		t.Fatalf("replacement was not aligned: %+v", rows[2])
	}
	if rows[3].left.text != "shared" || rows[3].right.text != "shared" {
		t.Fatalf("context was not aligned: %+v", rows[3])
	}
	if rows[4].left.present || rows[4].right.text != "extra" || rows[4].right.line != 3 {
		t.Fatalf("insertion was not aligned with an empty left cell: %+v", rows[4])
	}
}

func TestSideBySideViewPreservesPathIdentityAndPansLongLines(t *testing.T) {
	diff := copyDiff{
		sourcePath: "/same/long/prefix/.agents/skills/alpha",
		targetPath: "/same/long/prefix/.codex/skills/alpha",
		lines: []diffLine{
			{kind: diffHeader, path: "main.go"},
			{kind: diffHeader, path: "main.go"},
			{kind: diffDeletion, text: "-abcdefghijklmnopqrstuvwxyz", path: "main.go", oldLine: 1},
			{kind: diffAddition, text: "+ABCDEFGHIJKLMNOPQRSTUVWXYZ", path: "main.go", newLine: 1},
		},
	}
	initial := ansi.Strip(strings.Join(sideBySideDiffLines(diff, 58, 0), "\n"))
	panned := ansi.Strip(strings.Join(sideBySideDiffLines(diff, 58, 8), "\n"))
	if !strings.Contains(initial, ".agents/skills/alpha") || !strings.Contains(initial, ".codex/skills/alpha") {
		t.Fatalf("path suffixes do not identify each copy:\n%s", initial)
	}
	if initial == panned || !strings.Contains(panned, "‹") {
		t.Fatalf("horizontal pan did not reveal a different segment:\n%s", panned)
	}
}

func TestDiffTargetCyclingPrefersCanonicalCopy(t *testing.T) {
	m := model(t)
	source, first := diffFixture(t)
	second := t.TempDir()
	for _, root := range []string{source, first, second} {
		writeDiffFile(t, root, "SKILL.md", root+"\n")
	}
	m.result.Skills = []skills.Skill{
		{ID: "source", Name: "alpha", Path: source, ConflictID: "copies", ConflictKind: "divergent"},
		{ID: "canonical", Name: "alpha", Path: first, ConflictID: "copies", ConflictKind: "divergent", Canonical: true},
		{ID: "other", Name: "alpha", Path: second, ConflictID: "copies", ConflictKind: "divergent"},
	}
	m.restoreSelection("skill:source")
	_, target, ok := m.selectedDiffTarget()
	if !ok || target.ID != "canonical" {
		t.Fatal("comparison did not prefer the canonical copy")
	}
	m.pane = paneDiff
	next, cmd := key(m, "]")
	m = next
	_, target, ok = m.selectedDiffTarget()
	if !ok || target.ID != "other" || cmd == nil {
		t.Fatal("] did not select and load the next comparison copy")
	}
}

func diffFixture(t *testing.T) (string, string) {
	t.Helper()
	return t.TempDir(), t.TempDir()
}

func writeDiffFile(t *testing.T, root, path, content string) {
	t.Helper()
	writeDiffBytes(t, root, path, []byte(content))
}

func writeDiffBytes(t *testing.T, root, path string, content []byte) {
	t.Helper()
	fullPath := filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fullPath, content, 0644); err != nil {
		t.Fatal(err)
	}
}

func diffText(lines []diffLine) string {
	values := make([]string, len(lines))
	for i, line := range lines {
		values[i] = line.text
	}
	return strings.Join(values, "\n")
}

func hasDiffKind(lines []diffLine, kind diffLineKind) bool {
	for _, line := range lines {
		if line.kind == kind {
			return true
		}
	}
	return false
}
