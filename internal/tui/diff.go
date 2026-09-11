package tui

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/faizmokh/skmr/internal/skills"
	"github.com/faizmokh/skmr/internal/terminal"
)

const (
	maxInlineDiffBytes  = 256 * 1024
	maxInlineDiffLines  = 800
	maxInlineLineBytes  = 16 * 1024
	maxPackageDiffLines = 4000
)

type diffLineKind uint8

const (
	diffContext diffLineKind = iota
	diffAddition
	diffDeletion
	diffHeader
	diffHunk
	diffNotice
)

type diffLine struct {
	kind             diffLineKind
	text             string
	path             string
	oldLine, newLine int
}

type copyDiff struct {
	sourcePath string
	targetPath string
	lines      []diffLine
}

type diffMsg struct {
	sourceID string
	targetID string
	diff     copyDiff
	err      error
}

type packageEntry struct {
	kind   string
	size   int64
	target string
}

type lineOperation struct {
	kind diffLineKind
	text string
}

type sideDiffCell struct {
	kind    diffLineKind
	text    string
	path    string
	line    int
	present bool
}

type sideDiffRow struct {
	kind        diffLineKind
	label       string
	left, right sideDiffCell
}

func (m Model) diffPeers(source skills.Skill) []skills.Skill {
	peers := []skills.Skill{}
	if source.ConflictID == "" || !skills.HasCopyDifferences(source.ConflictKind) {
		return peers
	}
	for _, candidate := range m.result.Skills {
		if candidate.ConflictID == source.ConflictID && candidate.Path != source.Path {
			peers = append(peers, candidate)
		}
	}
	sort.Slice(peers, func(i, j int) bool {
		if peers[i].Canonical != peers[j].Canonical {
			return peers[i].Canonical
		}
		return peers[i].Path < peers[j].Path
	})
	return peers
}

func (m Model) selectedDiffTarget() (skills.Skill, skills.Skill, bool) {
	source, ok := m.selected()
	if !ok {
		return skills.Skill{}, skills.Skill{}, false
	}
	peers := m.diffPeers(source)
	if len(peers) == 0 {
		return skills.Skill{}, skills.Skill{}, false
	}
	index := min(max(0, m.diffTarget), len(peers)-1)
	return source, peers[index], true
}

func (m *Model) openDiff() tea.Cmd {
	if _, _, ok := m.selectedDiffTarget(); !ok {
		m.setNotice(noticeWarning, "Different copies are required before they can be compared.")
		return nil
	}
	m.openPane(paneDiff)
	m.diffTarget = 0
	m.diffHorizontal = 0
	m.setNotice(noticeProgress, "Comparing package contents…")
	return m.loadDiff()
}

func (m *Model) cycleDiffTarget(delta int) tea.Cmd {
	source, ok := m.selected()
	if !ok {
		return nil
	}
	peers := m.diffPeers(source)
	if len(peers) < 2 {
		return nil
	}
	m.diffTarget = (m.diffTarget + delta + len(peers)) % len(peers)
	m.diffHorizontal = 0
	m.offset = 0
	return m.loadDiff()
}

func (m *Model) loadDiff() tea.Cmd {
	source, target, ok := m.selectedDiffTarget()
	if !ok {
		return nil
	}
	m.diffLoading = true
	m.diff = copyDiff{sourcePath: source.Path, targetPath: target.Path}
	return func() tea.Msg {
		diff, err := buildCopyDiff(source.Path, target.Path)
		return diffMsg{sourceID: source.ID, targetID: target.ID, diff: diff, err: err}
	}
}

func buildCopyDiff(sourceRoot, targetRoot string) (copyDiff, error) {
	result := copyDiff{sourcePath: sourceRoot, targetPath: targetRoot}
	sourceEntries, err := readPackageEntries(sourceRoot)
	if err != nil {
		return result, err
	}
	targetEntries, err := readPackageEntries(targetRoot)
	if err != nil {
		return result, err
	}
	paths := map[string]bool{}
	for path := range sourceEntries {
		paths[path] = true
	}
	for path := range targetEntries {
		paths[path] = true
	}
	ordered := make([]string, 0, len(paths))
	for path := range paths {
		ordered = append(ordered, path)
	}
	sort.Strings(ordered)
	for _, path := range ordered {
		lines, err := diffPackageEntry(sourceRoot, targetRoot, path, sourceEntries[path], targetEntries[path])
		if err != nil {
			return result, err
		}
		result.lines = append(result.lines, lines...)
		if len(result.lines) > maxPackageDiffLines {
			result.lines = append(result.lines[:maxPackageDiffLines], diffLine{kind: diffNotice, text: "… More differences omitted."})
			break
		}
	}
	if len(result.lines) == 0 {
		result.lines = []diffLine{{kind: diffNotice, text: "No content differences found."}}
	}
	return result, nil
}

func readPackageEntries(root string) (map[string]packageEntry, error) {
	entries := map[string]packageEntry{}
	err := filepath.WalkDir(root, func(path string, item os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == "." || item.IsDir() {
			return nil
		}
		info, err := item.Info()
		if err != nil {
			return err
		}
		entry := packageEntry{kind: info.Mode().Type().String(), size: info.Size()}
		if info.Mode().IsRegular() {
			entry.kind = "file"
		}
		if info.Mode()&os.ModeSymlink != 0 {
			entry.kind = "symlink"
			entry.target, err = os.Readlink(path)
			if err != nil {
				return err
			}
		}
		entries[filepath.ToSlash(rel)] = entry
		return nil
	})
	return entries, err
}

func diffPackageEntry(sourceRoot, targetRoot, path string, source, target packageEntry) ([]diffLine, error) {
	sourceExists := source.kind != ""
	targetExists := target.kind != ""
	if sourceExists && targetExists && source.kind == target.kind {
		switch source.kind {
		case "symlink":
			if source.target == target.target {
				return nil, nil
			}
			return metadataDiff(path, "symlink to "+source.target, "symlink to "+target.target), nil
		case "file":
			return diffRegularFile(sourceRoot, targetRoot, path, source, target)
		default:
			if source.size == target.size {
				return nil, nil
			}
			return metadataDiff(path, source.kind, target.kind), nil
		}
	}
	if !sourceExists {
		if target.kind == "file" {
			return diffRegularFile(sourceRoot, targetRoot, path, packageEntry{}, target)
		}
		return metadataDiff(path, "missing", target.kind+" "+target.target), nil
	}
	if !targetExists {
		if source.kind == "file" {
			return diffRegularFile(sourceRoot, targetRoot, path, source, packageEntry{})
		}
		return metadataDiff(path, source.kind+" "+source.target, "missing"), nil
	}
	return metadataDiff(path, source.kind, target.kind), nil
}

func diffRegularFile(sourceRoot, targetRoot, path string, source, target packageEntry) ([]diffLine, error) {
	sourcePath, targetPath := filepath.Join(sourceRoot, filepath.FromSlash(path)), filepath.Join(targetRoot, filepath.FromSlash(path))
	if source.kind == "file" && target.kind == "file" {
		equal, err := regularFilesEqual(sourcePath, targetPath, source.size, target.size)
		if err != nil || equal {
			return nil, err
		}
	}
	header := []diffLine{
		{kind: diffHeader, text: "--- " + displayDiffPath("selected", path, source.kind == "file"), path: path},
		{kind: diffHeader, text: "+++ " + displayDiffPath("comparison", path, target.kind == "file"), path: path},
	}
	if source.size > maxInlineDiffBytes || target.size > maxInlineDiffBytes {
		return append(header, diffLine{kind: diffNotice, text: "Large file differs; inline diff omitted."}), nil
	}
	sourceContent, err := readOptionalFile(sourcePath, source.kind == "file")
	if err != nil {
		return nil, err
	}
	targetContent, err := readOptionalFile(targetPath, target.kind == "file")
	if err != nil {
		return nil, err
	}
	if bytes.IndexByte(sourceContent, 0) >= 0 || bytes.IndexByte(targetContent, 0) >= 0 {
		return append(header, diffLine{kind: diffNotice, text: "Binary file differs; inline diff omitted."}), nil
	}
	sourceLines, targetLines := textLines(sourceContent), textLines(targetContent)
	if len(sourceLines) > maxInlineDiffLines || len(targetLines) > maxInlineDiffLines {
		return append(header, diffLine{kind: diffNotice, text: fmt.Sprintf("Text file differs (%d vs %d lines); inline diff omitted.", len(sourceLines), len(targetLines))}), nil
	}
	if hasLongDiffLine(sourceLines) || hasLongDiffLine(targetLines) {
		return append(header, diffLine{kind: diffNotice, text: "Text file contains very long lines; inline diff omitted."}), nil
	}
	return append(header, unifiedLineDiff(path, sourceLines, targetLines)...), nil
}

func hasLongDiffLine(lines []string) bool {
	for _, line := range lines {
		if len(line) > maxInlineLineBytes {
			return true
		}
	}
	return false
}

func maxDiffLineWidth(lines []diffLine) int {
	width := 0
	for _, line := range lines {
		if line.kind != diffContext && line.kind != diffAddition && line.kind != diffDeletion {
			continue
		}
		text := strings.TrimPrefix(strings.TrimPrefix(line.text, "+"), "-")
		width = max(width, len([]rune(terminal.Safe(text))))
	}
	return width
}

func regularFilesEqual(sourcePath, targetPath string, sourceSize, targetSize int64) (bool, error) {
	if sourceSize != targetSize {
		return false, nil
	}
	sourceDigest, err := fileHash(sourcePath)
	if err != nil {
		return false, err
	}
	targetDigest, err := fileHash(targetPath)
	return sourceDigest == targetDigest, err
}

func fileHash(path string) ([sha256.Size]byte, error) {
	var digest [sha256.Size]byte
	file, err := os.Open(path)
	if err != nil {
		return digest, err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err = io.Copy(hash, file); err != nil {
		return digest, err
	}
	copy(digest[:], hash.Sum(nil))
	return digest, nil
}

func readOptionalFile(path string, exists bool) ([]byte, error) {
	if !exists {
		return nil, nil
	}
	return os.ReadFile(path)
}

func displayDiffPath(side, path string, exists bool) string {
	if !exists {
		return "/dev/null"
	}
	return side + "/" + path
}

func metadataDiff(path, source, target string) []diffLine {
	return []diffLine{
		{kind: diffHeader, text: "--- selected/" + path, path: path},
		{kind: diffHeader, text: "+++ comparison/" + path, path: path},
		{kind: diffHunk, text: "@@ entry @@", path: path},
		{kind: diffDeletion, text: "-" + strings.TrimSpace(source), path: path},
		{kind: diffAddition, text: "+" + strings.TrimSpace(target), path: path},
	}
}

func textLines(content []byte) []string {
	if len(content) == 0 {
		return nil
	}
	text := strings.ReplaceAll(string(content), "\r\n", "\n")
	text = strings.TrimSuffix(text, "\n")
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

func unifiedLineDiff(path string, source, target []string) []diffLine {
	operations := lineDiffOperations(source, target)
	changed := []int{}
	for i, operation := range operations {
		if operation.kind != diffContext {
			changed = append(changed, i)
		}
	}
	if len(changed) == 0 {
		return nil
	}
	ranges := [][2]int{}
	for _, index := range changed {
		start, end := max(0, index-3), min(len(operations), index+4)
		if len(ranges) > 0 && start <= ranges[len(ranges)-1][1] {
			ranges[len(ranges)-1][1] = max(ranges[len(ranges)-1][1], end)
		} else {
			ranges = append(ranges, [2]int{start, end})
		}
	}
	lines := []diffLine{}
	for _, span := range ranges {
		oldStart, newStart := 1, 1
		for _, operation := range operations[:span[0]] {
			if operation.kind != diffAddition {
				oldStart++
			}
			if operation.kind != diffDeletion {
				newStart++
			}
		}
		oldCount, newCount := 0, 0
		for _, operation := range operations[span[0]:span[1]] {
			if operation.kind != diffAddition {
				oldCount++
			}
			if operation.kind != diffDeletion {
				newCount++
			}
		}
		lines = append(lines, diffLine{kind: diffHunk, text: fmt.Sprintf("@@ -%d,%d +%d,%d @@", oldStart, oldCount, newStart, newCount), path: path})
		oldLine, newLine := oldStart, newStart
		for _, operation := range operations[span[0]:span[1]] {
			prefix := " "
			if operation.kind == diffAddition {
				prefix = "+"
			} else if operation.kind == diffDeletion {
				prefix = "-"
			}
			line := diffLine{kind: operation.kind, text: prefix + operation.text, path: path}
			if operation.kind != diffAddition {
				line.oldLine = oldLine
				oldLine++
			}
			if operation.kind != diffDeletion {
				line.newLine = newLine
				newLine++
			}
			lines = append(lines, line)
		}
	}
	return lines
}

func sideBySideRows(lines []diffLine) []sideDiffRow {
	rows := []sideDiffRow{}
	for i := 0; i < len(lines); {
		line := lines[i]
		switch line.kind {
		case diffHeader:
			rows = append(rows, sideDiffRow{kind: diffHeader, label: line.path})
			i++
			if i < len(lines) && lines[i].kind == diffHeader && lines[i].path == line.path {
				i++
			}
		case diffHunk, diffNotice:
			rows = append(rows, sideDiffRow{kind: line.kind, label: line.text})
			i++
		case diffContext:
			text := strings.TrimPrefix(line.text, " ")
			rows = append(rows, sideDiffRow{
				left:  sideDiffCell{kind: diffContext, text: text, path: line.path, line: line.oldLine, present: true},
				right: sideDiffCell{kind: diffContext, text: text, path: line.path, line: line.newLine, present: true},
			})
			i++
		default:
			deletions, additions := []diffLine{}, []diffLine{}
			for i < len(lines) && (lines[i].kind == diffDeletion || lines[i].kind == diffAddition) {
				if lines[i].kind == diffDeletion {
					deletions = append(deletions, lines[i])
				} else {
					additions = append(additions, lines[i])
				}
				i++
			}
			for index := 0; index < max(len(deletions), len(additions)); index++ {
				row := sideDiffRow{}
				if index < len(deletions) {
					item := deletions[index]
					row.left = sideDiffCell{kind: diffDeletion, text: strings.TrimPrefix(item.text, "-"), path: item.path, line: item.oldLine, present: true}
				}
				if index < len(additions) {
					item := additions[index]
					row.right = sideDiffCell{kind: diffAddition, text: strings.TrimPrefix(item.text, "+"), path: item.path, line: item.newLine, present: true}
				}
				rows = append(rows, row)
			}
		}
	}
	return rows
}

func lineDiffOperations(source, target []string) []lineOperation {
	columns := len(target) + 1
	table := make([]int, (len(source)+1)*columns)
	for i := len(source) - 1; i >= 0; i-- {
		for j := len(target) - 1; j >= 0; j-- {
			index := i*columns + j
			if source[i] == target[j] {
				table[index] = table[(i+1)*columns+j+1] + 1
			} else {
				table[index] = max(table[(i+1)*columns+j], table[i*columns+j+1])
			}
		}
	}
	operations := []lineOperation{}
	for i, j := 0, 0; i < len(source) || j < len(target); {
		switch {
		case i < len(source) && j < len(target) && source[i] == target[j]:
			operations = append(operations, lineOperation{kind: diffContext, text: source[i]})
			i++
			j++
		case j < len(target) && (i == len(source) || table[i*columns+j+1] > table[(i+1)*columns+j]):
			operations = append(operations, lineOperation{kind: diffAddition, text: target[j]})
			j++
		default:
			operations = append(operations, lineOperation{kind: diffDeletion, text: source[i]})
			i++
		}
	}
	return operations
}
