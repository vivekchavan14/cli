package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/omnitrix-sh/cli/internals/diff"
	"github.com/omnitrix-sh/cli/internals/lsp"
	permission "github.com/omnitrix-sh/cli/internals/permissions"
)

type PatchParams struct {
	PatchText string `json:"patch_text"`
}

type PatchResponseMetadata struct {
	FilesChanged []string `json:"files_changed"`
	Additions    int      `json:"additions"`
	Removals     int      `json:"removals"`
}

type patchTool struct {
	lspClients  map[string]*lsp.Client
	permissions permission.Service
}

const (
	PatchToolName    = "patch"
	patchDescription = `Applies a patch to multiple files in one atomic operation.

The patch text must follow this format:
*** Begin Patch
*** Update File: /path/to/file
@@ Context line (unique within the file)
 Line to keep
-Line to remove
+Line to add
 Line to keep
*** Add File: /path/to/new/file
+Content of the new file
+More content
*** Delete File: /path/to/file/to/delete
*** End Patch

CRITICAL REQUIREMENTS:
1. UNIQUENESS: Context lines MUST uniquely identify the specific sections you want to change
2. PRECISION: All whitespace, indentation, and surrounding code must match exactly
3. VALIDATION: Ensure edits result in idiomatic, correct code
4. PATHS: Always use absolute file paths (starting with /)

The tool will apply all changes in a single atomic operation.`
)

func NewPatchTool(lspClients map[string]*lsp.Client, permissions permission.Service) BaseTool {
	return &patchTool{
		lspClients:  lspClients,
		permissions: permissions,
	}
}

func (p *patchTool) Info() ToolInfo {
	return ToolInfo{
		Name:        PatchToolName,
		Description: patchDescription,
		Parameters: map[string]any{
			"patch_text": map[string]any{
				"type":        "string",
				"description": "The full patch text that describes all changes to be made",
			},
		},
		Required: []string{"patch_text"},
	}
}

func (p *patchTool) Run(ctx context.Context, call ToolCall) (ToolResponse, error) {
	var params PatchParams
	if err := json.Unmarshal([]byte(call.Input), &params); err != nil {
		return NewTextErrorResponse("invalid parameters"), nil
	}

	if params.PatchText == "" {
		return NewTextErrorResponse("patch_text is required"), nil
	}

	// Identify all files needed for the patch
	filesToRead := diff.IdentifyFilesNeeded(params.PatchText)
	for _, filePath := range filesToRead {
		absPath := filePath
		if !filepath.IsAbs(absPath) {
			wd, _ := os.Getwd()
			absPath = filepath.Join(wd, absPath)
		}

		fileInfo, err := os.Stat(absPath)
		if err != nil {
			if os.IsNotExist(err) {
				return NewTextErrorResponse(fmt.Sprintf("file not found: %s", filePath)), nil
			}
			return ToolResponse{}, fmt.Errorf("failed to access file: %w", err)
		}

		if fileInfo.IsDir() {
			return NewTextErrorResponse(fmt.Sprintf("path is a directory, not a file: %s", absPath)), nil
		}
	}

	// Check for new files to ensure they don't already exist
	filesToAdd := diff.IdentifyFilesAdded(params.PatchText)
	for _, filePath := range filesToAdd {
		absPath := filePath
		if !filepath.IsAbs(absPath) {
			wd, _ := os.Getwd()
			absPath = filepath.Join(wd, absPath)
		}

		_, err := os.Stat(absPath)
		if err == nil {
			return NewTextErrorResponse(fmt.Sprintf("file already exists and cannot be added: %s", absPath)), nil
		} else if !os.IsNotExist(err) {
			return ToolResponse{}, fmt.Errorf("failed to check file: %w", err)
		}
	}

	// Load all required files
	currentFiles := make(map[string]string)
	for _, filePath := range filesToRead {
		absPath := filePath
		if !filepath.IsAbs(absPath) {
			wd, _ := os.Getwd()
			absPath = filepath.Join(wd, absPath)
		}

		content, err := os.ReadFile(absPath)
		if err != nil {
			return ToolResponse{}, fmt.Errorf("failed to read file %s: %w", absPath, err)
		}
		currentFiles[filePath] = string(content)
	}

	// Process the patch
	patch, fuzz, err := diff.TextToPatch(params.PatchText, currentFiles)
	if err != nil {
		return NewTextErrorResponse(fmt.Sprintf("failed to parse patch: %s", err)), nil
	}

	if fuzz > 3 {
		return NewTextErrorResponse(fmt.Sprintf("patch contains fuzzy matches (fuzz level: %d). Please make your context lines more precise", fuzz)), nil
	}

	// Convert patch to commit
	commit, err := diff.PatchToCommit(patch, currentFiles)
	if err != nil {
		return NewTextErrorResponse(fmt.Sprintf("failed to create commit from patch: %s", err)), nil
	}

	// Request permission for all changes
	for path, change := range commit.Changes {
		switch change.Type {
		case diff.ActionAdd:
			dir := filepath.Dir(path)
			patchDiff, _, _ := diff.GenerateDiff("", *change.NewContent, path)
			p := p.permissions.Request(
				permission.CreatePermissionRequest{
					Path:        dir,
					ToolName:    PatchToolName,
					Action:      "create",
					Description: fmt.Sprintf("create file %s", path),
					Params:      patchDiff,
				},
			)
			if !p {
				return NewTextErrorResponse(fmt.Sprintf("permission denied for creating file: %s", path)), nil
			}
		case diff.ActionDelete:
			dir := filepath.Dir(path)
			patchDiff, _, _ := diff.GenerateDiff(*change.OldContent, "", path)
			p := p.permissions.Request(
				permission.CreatePermissionRequest{
					Path:        dir,
					ToolName:    PatchToolName,
					Action:      "delete",
					Description: fmt.Sprintf("delete file %s", path),
					Params:      patchDiff,
				},
			)
			if !p {
				return NewTextErrorResponse(fmt.Sprintf("permission denied for deleting file: %s", path)), nil
			}
		case diff.ActionUpdate:
			dir := filepath.Dir(path)
			patchDiff, _, _ := diff.GenerateDiff(*change.OldContent, *change.NewContent, path)
			p := p.permissions.Request(
				permission.CreatePermissionRequest{
					Path:        dir,
					ToolName:    PatchToolName,
					Action:      "modify",
					Description: fmt.Sprintf("modify file %s", path),
					Params:      patchDiff,
				},
			)
			if !p {
				return NewTextErrorResponse(fmt.Sprintf("permission denied for modifying file: %s", path)), nil
			}
		}
	}

	// Apply changes atomically
	filesChanged := make([]string, 0)
	additions := 0
	removals := 0

	for path, change := range commit.Changes {
		absPath := path
		if !filepath.IsAbs(absPath) {
			wd, _ := os.Getwd()
			absPath = filepath.Join(wd, absPath)
		}

		switch change.Type {
		case diff.ActionAdd:
			// Create parent directories if they don't exist
			dir := filepath.Dir(absPath)
			if err := os.MkdirAll(dir, 0755); err != nil {
				return ToolResponse{}, fmt.Errorf("failed to create directories for %s: %w", path, err)
			}

			// Write new file
			if err := os.WriteFile(absPath, []byte(*change.NewContent), 0644); err != nil {
				return ToolResponse{}, fmt.Errorf("failed to write file %s: %w", path, err)
			}
			filesChanged = append(filesChanged, path)
			additions += len(*change.NewContent)

		case diff.ActionDelete:
			// Delete file
			if err := os.Remove(absPath); err != nil {
				return ToolResponse{}, fmt.Errorf("failed to delete file %s: %w", path, err)
			}
			filesChanged = append(filesChanged, path)
			removals += len(*change.OldContent)

		case diff.ActionUpdate:
			// Write updated file
			if err := os.WriteFile(absPath, []byte(*change.NewContent), 0644); err != nil {
				return ToolResponse{}, fmt.Errorf("failed to write file %s: %w", path, err)
			}
			filesChanged = append(filesChanged, path)

			// Calculate additions/removals
			_, add, remove := diff.GenerateDiff(*change.OldContent, *change.NewContent, path)
			additions += add
			removals += remove
		}
	}

	// Note: LSP client notifications for file changes would go here
	// The LSP protocol will detect the file changes through the file system watcher

	metadata := PatchResponseMetadata{
		FilesChanged: filesChanged,
		Additions:    additions,
		Removals:     removals,
	}

	metadataJSON, _ := json.Marshal(metadata)
	result := fmt.Sprintf("Successfully applied patch to %d files\nAdditions: %d, Removals: %d\nFiles changed: %v",
		len(filesChanged), additions, removals, filesChanged)

	response := ToolResponse{
		Type:    "text",
		Content: result,
		IsError: false,
	}

	// Include metadata as comment
	response.Content += "\n\nMetadata: " + string(metadataJSON)
	return response, nil
}
