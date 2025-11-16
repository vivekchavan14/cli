package diff

import (
	"fmt"
	"strings"
)

type Parser struct {
	currentFiles map[string]string
	lines        []string
	index        int
	patch        Patch
	fuzz         int
}

func NewParser(currentFiles map[string]string, lines []string) *Parser {
	return &Parser{
		currentFiles: currentFiles,
		lines:        lines,
		index:        0,
		patch:        Patch{Actions: make(map[string]PatchAction, len(currentFiles))},
		fuzz:         0,
	}
}

func (p *Parser) isDone(prefixes []string) bool {
	if p.index >= len(p.lines) {
		return true
	}
	for _, prefix := range prefixes {
		if strings.HasPrefix(p.lines[p.index], prefix) {
			return true
		}
	}
	return false
}

func (p *Parser) startsWith(prefix any) bool {
	var prefixes []string
	switch v := prefix.(type) {
	case string:
		prefixes = []string{v}
	case []string:
		prefixes = v
	}

	for _, pfx := range prefixes {
		if strings.HasPrefix(p.lines[p.index], pfx) {
			return true
		}
	}
	return false
}

func (p *Parser) readStr(prefix string, returnEverything bool) string {
	if p.index >= len(p.lines) {
		return ""
	}
	if strings.HasPrefix(p.lines[p.index], prefix) {
		var text string
		if returnEverything {
			text = p.lines[p.index]
		} else {
			text = p.lines[p.index][len(prefix):]
		}
		p.index++
		return text
	}
	return ""
}

func (p *Parser) Parse() error {
	endPatchPrefixes := []string{"*** End Patch"}

	for !p.isDone(endPatchPrefixes) {
		path := p.readStr("*** Update File: ", false)
		if path != "" {
			if _, exists := p.patch.Actions[path]; exists {
				return fileError("Update", "Duplicate Path", path)
			}
			moveTo := p.readStr("*** Move to: ", false)
			if _, exists := p.currentFiles[path]; !exists {
				return fileError("Update", "Missing File", path)
			}
			text := p.currentFiles[path]
			action, err := p.parseUpdateFile(text)
			if err != nil {
				return err
			}
			if moveTo != "" {
				action.MovePath = &moveTo
			}
			p.patch.Actions[path] = action
			continue
		}

		path = p.readStr("*** Delete File: ", false)
		if path != "" {
			if _, exists := p.patch.Actions[path]; exists {
				return fileError("Delete", "Duplicate Path", path)
			}
			if _, exists := p.currentFiles[path]; !exists {
				return fileError("Delete", "Missing File", path)
			}
			p.patch.Actions[path] = PatchAction{Type: ActionDelete, Chunks: []Chunk{}}
			continue
		}

		path = p.readStr("*** Add File: ", false)
		if path != "" {
			if _, exists := p.patch.Actions[path]; exists {
				return fileError("Add", "Duplicate Path", path)
			}
			if _, exists := p.currentFiles[path]; exists {
				return fileError("Add", "File already exists", path)
			}
			action, err := p.parseAddFile()
			if err != nil {
				return err
			}
			p.patch.Actions[path] = action
			continue
		}

		return NewDiffError(fmt.Sprintf("Unknown Line: %s", p.lines[p.index]))
	}

	if !p.startsWith("*** End Patch") {
		return NewDiffError("Missing End Patch")
	}
	p.index++

	return nil
}

func (p *Parser) parseUpdateFile(text string) (PatchAction, error) {
	action := PatchAction{Type: ActionUpdate, Chunks: []Chunk{}}
	fileLines := strings.Split(text, "\n")
	index := 0

	endPrefixes := []string{
		"*** End Patch",
		"*** Update File:",
		"*** Delete File:",
		"*** Add File:",
		"*** End of File",
	}

	for !p.isDone(endPrefixes) {
		defStr := p.readStr("@@ ", false)
		sectionStr := ""
		if defStr == "" && p.index < len(p.lines) && p.lines[p.index] == "@@" {
			sectionStr = p.lines[p.index]
			p.index++
		}
		if defStr == "" && sectionStr == "" && index != 0 {
			return action, NewDiffError(fmt.Sprintf("Invalid Line:\n%s", p.lines[p.index]))
		}

		if strings.TrimSpace(defStr) != "" {
			found := false
			for i := range fileLines[:index] {
				if fileLines[i] == defStr {
					found = true
					break
				}
			}

			if !found {
				for i := index; i < len(fileLines); i++ {
					if fileLines[i] == defStr {
						index = i + 1
						found = true
						break
					}
				}
			}

			if !found {
				for i := range fileLines[:index] {
					if strings.TrimSpace(fileLines[i]) == strings.TrimSpace(defStr) {
						found = true
						break
					}
				}
			}

			if !found {
				for i := index; i < len(fileLines); i++ {
					if strings.TrimSpace(fileLines[i]) == strings.TrimSpace(defStr) {
						index = i + 1
						p.fuzz++
						found = true
						break
					}
				}
			}
		}

		nextChunkContext, chunks, endPatchIndex, eof := peekNextSection(p.lines, p.index)
		newIndex, fuzz := findContext(fileLines, nextChunkContext, index, eof)
		if newIndex == -1 {
			ctxText := strings.Join(nextChunkContext, "\n")
			return action, contextError(index, ctxText, eof)
		}
		p.fuzz += fuzz

		for _, ch := range chunks {
			ch.OrigIndex += newIndex
			action.Chunks = append(action.Chunks, ch)
		}
		index = newIndex + len(nextChunkContext)
		p.index = endPatchIndex
	}
	return action, nil
}

func (p *Parser) parseAddFile() (PatchAction, error) {
	lines := make([]string, 0, 16)
	endPrefixes := []string{
		"*** End Patch",
		"*** Update File:",
		"*** Delete File:",
		"*** Add File:",
	}

	for !p.isDone(endPrefixes) {
		s := p.readStr("", true)
		if !strings.HasPrefix(s, "+") {
			return PatchAction{}, NewDiffError(fmt.Sprintf("Invalid Add File Line: %s", s))
		}
		lines = append(lines, s[1:])
	}

	newFile := strings.Join(lines, "\n")
	return PatchAction{
		Type:    ActionAdd,
		NewFile: &newFile,
		Chunks:  []Chunk{},
	}, nil
}

// Helper functions for matching
func findContextCore(lines []string, context []string, start int) (int, int) {
	if len(context) == 0 {
		return start, 0
	}

	// Try exact match
	if idx, fuzz := tryFindMatch(lines, context, start, func(a, b string) bool {
		return a == b
	}); idx >= 0 {
		return idx, fuzz
	}

	// Try trimming right whitespace
	if idx, fuzz := tryFindMatch(lines, context, start, func(a, b string) bool {
		return strings.TrimRight(a, " \t") == strings.TrimRight(b, " \t")
	}); idx >= 0 {
		return idx, fuzz
	}

	// Try trimming all whitespace
	if idx, fuzz := tryFindMatch(lines, context, start, func(a, b string) bool {
		return strings.TrimSpace(a) == strings.TrimSpace(b)
	}); idx >= 0 {
		return idx, fuzz
	}

	return -1, 0
}

func tryFindMatch(lines []string, context []string, start int,
	compareFunc func(string, string) bool,
) (int, int) {
	for i := start; i < len(lines); i++ {
		if i+len(context) <= len(lines) {
			match := true
			for j := range context {
				if !compareFunc(lines[i+j], context[j]) {
					match = false
					break
				}
			}
			if match {
				var fuzz int
				if compareFunc("a ", "a") && !compareFunc("a", "b") {
					fuzz = 1
				} else if compareFunc("a  ", "a") {
					fuzz = 100
				}
				return i, fuzz
			}
		}
	}
	return -1, 0
}

func findContext(lines []string, context []string, start int, eof bool) (int, int) {
	if eof {
		newIndex, fuzz := findContextCore(lines, context, len(lines)-len(context))
		if newIndex != -1 {
			return newIndex, fuzz
		}
		newIndex, fuzz = findContextCore(lines, context, start)
		return newIndex, fuzz + 10000
	}
	return findContextCore(lines, context, start)
}

func peekNextSection(lines []string, initialIndex int) ([]string, []Chunk, int, bool) {
	index := initialIndex
	old := make([]string, 0, 32)
	delLines := make([]string, 0, 8)
	insLines := make([]string, 0, 8)
	chunks := make([]Chunk, 0, 4)
	mode := "keep"

	endSectionConditions := func(s string) bool {
		return strings.HasPrefix(s, "@@") ||
			strings.HasPrefix(s, "*** End Patch") ||
			strings.HasPrefix(s, "*** Update File:") ||
			strings.HasPrefix(s, "*** Delete File:") ||
			strings.HasPrefix(s, "*** Add File:") ||
			strings.HasPrefix(s, "*** End of File") ||
			s == "***" ||
			strings.HasPrefix(s, "***")
	}

	for index < len(lines) {
		s := lines[index]
		if endSectionConditions(s) {
			break
		}
		index++
		lastMode := mode
		line := s

		if len(line) > 0 {
			switch line[0] {
			case '+':
				mode = "add"
			case '-':
				mode = "delete"
			case ' ':
				mode = "keep"
			default:
				mode = "keep"
				line = " " + line
			}
		} else {
			mode = "keep"
			line = " "
		}

		line = line[1:]
		if mode == "keep" && lastMode != mode {
			if len(insLines) > 0 || len(delLines) > 0 {
				chunks = append(chunks, Chunk{
					OrigIndex: len(old) - len(delLines),
					DelLines:  delLines,
					InsLines:  insLines,
				})
			}
			delLines = make([]string, 0, 8)
			insLines = make([]string, 0, 8)
		}
		switch mode {
		case "delete":
			delLines = append(delLines, line)
			old = append(old, line)
		case "add":
			insLines = append(insLines, line)
		default:
			old = append(old, line)
		}
	}

	if len(insLines) > 0 || len(delLines) > 0 {
		chunks = append(chunks, Chunk{
			OrigIndex: len(old) - len(delLines),
			DelLines:  delLines,
			InsLines:  insLines,
		})
	}

	if index < len(lines) && lines[index] == "*** End of File" {
		index++
		return old, chunks, index, true
	}
	return old, chunks, index, false
}

func TextToPatch(text string, orig map[string]string) (Patch, int, error) {
	text = strings.TrimSpace(text)
	lines := strings.Split(text, "\n")
	if len(lines) < 2 || !strings.HasPrefix(lines[0], "*** Begin Patch") || lines[len(lines)-1] != "*** End Patch" {
		return Patch{}, 0, NewDiffError("Invalid patch text")
	}
	parser := NewParser(orig, lines)
	parser.index = 1
	if err := parser.Parse(); err != nil {
		return Patch{}, 0, err
	}
	return parser.patch, parser.fuzz, nil
}

func IdentifyFilesNeeded(text string) []string {
	text = strings.TrimSpace(text)
	lines := strings.Split(text, "\n")
	result := make(map[string]bool)

	for _, line := range lines {
		if strings.HasPrefix(line, "*** Update File: ") {
			result[line[len("*** Update File: "):]] = true
		}
		if strings.HasPrefix(line, "*** Delete File: ") {
			result[line[len("*** Delete File: "):]] = true
		}
	}

	files := make([]string, 0, len(result))
	for file := range result {
		files = append(files, file)
	}
	return files
}

func IdentifyFilesAdded(text string) []string {
	text = strings.TrimSpace(text)
	lines := strings.Split(text, "\n")
	result := make(map[string]bool)

	for _, line := range lines {
		if strings.HasPrefix(line, "*** Add File: ") {
			result[line[len("*** Add File: "):]] = true
		}
	}

	files := make([]string, 0, len(result))
	for file := range result {
		files = append(files, file)
	}
	return files
}

func PatchToCommit(patch Patch, currentFiles map[string]string) (Commit, error) {
	commit := Commit{Changes: make(map[string]FileChange)}

	for path, action := range patch.Actions {
		change := FileChange{}

		switch action.Type {
		case ActionAdd:
			if action.NewFile == nil {
				return Commit{}, NewDiffError(fmt.Sprintf("Add action for %s missing NewFile", path))
			}
			change.Type = ActionAdd
			change.NewContent = action.NewFile

		case ActionDelete:
			oldContent := currentFiles[path]
			change.Type = ActionDelete
			change.OldContent = &oldContent

		case ActionUpdate:
			if content, exists := currentFiles[path]; exists {
				newContent := applyChunks(content, action.Chunks)
				change.Type = ActionUpdate
				change.OldContent = &content
				change.NewContent = &newContent
			} else {
				return Commit{}, NewDiffError(fmt.Sprintf("Update action for missing file: %s", path))
			}
		}

		if action.MovePath != nil {
			change.MovePath = action.MovePath
		}

		commit.Changes[path] = change
	}

	return commit, nil
}

func applyChunks(content string, chunks []Chunk) string {
	lines := strings.Split(content, "\n")

	// Apply chunks in reverse order to maintain indices
	for i := len(chunks) - 1; i >= 0; i-- {
		chunk := chunks[i]
		startIdx := chunk.OrigIndex
		endIdx := startIdx + len(chunk.DelLines)

		// Delete old lines
		newLines := make([]string, 0, len(lines)-len(chunk.DelLines)+len(chunk.InsLines))
		newLines = append(newLines, lines[:startIdx]...)
		newLines = append(newLines, chunk.InsLines...)
		newLines = append(newLines, lines[endIdx:]...)
		lines = newLines
	}

	return strings.Join(lines, "\n")
}

func GenerateDiff(oldContent, newContent, filePath string) (string, int, int) {
	oldLines := strings.Split(oldContent, "\n")
	newLines := strings.Split(newContent, "\n")

	additions := 0
	removals := 0

	for _, line := range oldLines {
		found := false
		for _, newLine := range newLines {
			if line == newLine {
				found = true
				break
			}
		}
		if !found && line != "" {
			removals++
		}
	}

	for _, line := range newLines {
		found := false
		for _, oldLine := range oldLines {
			if line == oldLine {
				found = true
				break
			}
		}
		if !found && line != "" {
			additions++
		}
	}

	diff := fmt.Sprintf("--- a/%s\n+++ b/%s\n", filePath, filePath)
	return diff, additions, removals
}
