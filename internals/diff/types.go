package diff

type ActionType string

const (
	ActionAdd    ActionType = "add"
	ActionDelete ActionType = "delete"
	ActionUpdate ActionType = "update"
)

type FileChange struct {
	Type       ActionType
	OldContent *string
	NewContent *string
	MovePath   *string
}

type Commit struct {
	Changes map[string]FileChange
}

type Chunk struct {
	OrigIndex int      // line index of the first line in the original file
	DelLines  []string // lines to delete
	InsLines  []string // lines to insert
}

type PatchAction struct {
	Type     ActionType
	NewFile  *string
	Chunks   []Chunk
	MovePath *string
}

type Patch struct {
	Actions map[string]PatchAction
}

type DiffError struct {
	message string
}

func (e DiffError) Error() string {
	return e.message
}

func NewDiffError(message string) DiffError {
	return DiffError{message: message}
}

func fileError(action, reason, path string) DiffError {
	return NewDiffError(action + " File Error: " + reason + ": " + path)
}

func contextError(index int, context string, isEOF bool) DiffError {
	prefix := "Invalid Context"
	if isEOF {
		prefix = "Invalid EOF Context"
	}
	return NewDiffError(prefix + " at index " + string(rune(index)) + ":\n" + context)
}
