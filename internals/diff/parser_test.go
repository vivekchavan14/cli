package diff

import (
	"strings"
	"testing"
)

func TestIdentifyFilesNeeded(t *testing.T) {
	patchText := `*** Begin Patch
*** Update File: /path/to/file1.go
@@ context line
 some code
-remove this
+add this
*** Update File: /path/to/file2.go
@@ another context
 more code
*** Delete File: /path/to/file3.go
*** End Patch`

	files := IdentifyFilesNeeded(patchText)
	if len(files) != 3 {
		t.Errorf("Expected 3 files, got %d", len(files))
	}
}

func TestIdentifyFilesAdded(t *testing.T) {
	patchText := `*** Begin Patch
*** Add File: /path/to/newfile1.go
+package main
+func main() {}
*** Add File: /path/to/newfile2.go
+another file
*** End Patch`

	files := IdentifyFilesAdded(patchText)
	if len(files) != 2 {
		t.Errorf("Expected 2 files to add, got %d", len(files))
	}
}

func TestTextToPatchSimpleUpdate(t *testing.T) {
	patchText := `*** Begin Patch
*** Update File: /test.txt
@@ original line
 line 1
-old content
+new content
 line 3
*** End Patch`

	currentFiles := map[string]string{
		"/test.txt": "line 1\nold content\nline 3\n",
	}

	patch, fuzz, err := TextToPatch(patchText, currentFiles)
	if err != nil {
		t.Fatalf("Failed to parse patch: %v", err)
	}

	if len(patch.Actions) != 1 {
		t.Errorf("Expected 1 action, got %d", len(patch.Actions))
	}

	if fuzz > 0 {
		t.Errorf("Expected 0 fuzz, got %d", fuzz)
	}

	action, exists := patch.Actions["/test.txt"]
	if !exists {
		t.Fatalf("Expected /test.txt in actions")
	}

	if action.Type != ActionUpdate {
		t.Errorf("Expected ActionUpdate, got %v", action.Type)
	}

	if len(action.Chunks) == 0 {
		t.Errorf("Expected chunks, got none")
	}
}

func TestTextToPatchAddFile(t *testing.T) {
	patchText := `*** Begin Patch
*** Add File: /newfile.txt
+line 1
+line 2
+line 3
*** End Patch`

	currentFiles := map[string]string{}

	patch, _, err := TextToPatch(patchText, currentFiles)
	if err != nil {
		t.Fatalf("Failed to parse patch: %v", err)
	}

	if len(patch.Actions) != 1 {
		t.Errorf("Expected 1 action, got %d", len(patch.Actions))
	}

	action, exists := patch.Actions["/newfile.txt"]
	if !exists {
		t.Fatalf("Expected /newfile.txt in actions")
	}

	if action.Type != ActionAdd {
		t.Errorf("Expected ActionAdd, got %v", action.Type)
	}

	if action.NewFile == nil {
		t.Fatalf("Expected NewFile content")
	}

	expected := "line 1\nline 2\nline 3"
	if *action.NewFile != expected {
		t.Errorf("Expected %q, got %q", expected, *action.NewFile)
	}
}

func TestTextToPatchDeleteFile(t *testing.T) {
	patchText := `*** Begin Patch
*** Delete File: /oldfile.txt
*** End Patch`

	currentFiles := map[string]string{
		"/oldfile.txt": "old content\n",
	}

	patch, _, err := TextToPatch(patchText, currentFiles)
	if err != nil {
		t.Fatalf("Failed to parse patch: %v", err)
	}

	if len(patch.Actions) != 1 {
		t.Errorf("Expected 1 action, got %d", len(patch.Actions))
	}

	action, exists := patch.Actions["/oldfile.txt"]
	if !exists {
		t.Fatalf("Expected /oldfile.txt in actions")
	}

	if action.Type != ActionDelete {
		t.Errorf("Expected ActionDelete, got %v", action.Type)
	}
}

func TestPatchToCommit(t *testing.T) {
	patch := Patch{
		Actions: map[string]PatchAction{
			"/test.txt": {
				Type: ActionUpdate,
				Chunks: []Chunk{
					{
						OrigIndex: 1,
						DelLines:  []string{"old"},
						InsLines:  []string{"new"},
					},
				},
			},
		},
	}

	currentFiles := map[string]string{
		"/test.txt": "line1\nold\nline3\n",
	}

	commit, err := PatchToCommit(patch, currentFiles)
	if err != nil {
		t.Fatalf("Failed to convert patch to commit: %v", err)
	}

	if len(commit.Changes) != 1 {
		t.Errorf("Expected 1 change, got %d", len(commit.Changes))
	}

	change, exists := commit.Changes["/test.txt"]
	if !exists {
		t.Fatalf("Expected /test.txt in changes")
	}

	if change.Type != ActionUpdate {
		t.Errorf("Expected ActionUpdate, got %v", change.Type)
	}

	if !strings.Contains(*change.NewContent, "new") {
		t.Errorf("Expected 'new' in new content")
	}
}

func TestGenerateDiff(t *testing.T) {
	oldContent := "line1\nold line\nline3\n"
	newContent := "line1\nnew line\nline3\n"

	diff, additions, removals := GenerateDiff(oldContent, newContent, "/test.txt")

	if !strings.Contains(diff, "/test.txt") {
		t.Errorf("Expected file path in diff")
	}

	if additions == 0 {
		t.Errorf("Expected additions > 0")
	}

	if removals == 0 {
		t.Errorf("Expected removals > 0")
	}
}

func TestTextToPatchInvalidFormat(t *testing.T) {
	patchText := `This is not a valid patch`

	currentFiles := map[string]string{}

	_, _, err := TextToPatch(patchText, currentFiles)
	if err == nil {
		t.Fatalf("Expected error for invalid patch format")
	}
}

func TestApplyChunks(t *testing.T) {
	content := "line1\nline2\nline3\nline4\nline5\n"
	chunks := []Chunk{
		{
			OrigIndex: 1,
			DelLines:  []string{"line2"},
			InsLines:  []string{"new line"},
		},
	}

	result := applyChunks(content, chunks)

	if !strings.Contains(result, "new line") {
		t.Errorf("Expected 'new line' in result")
	}

	if strings.Contains(result, "line2") {
		t.Errorf("Did not expect 'line2' in result after deletion")
	}
}
