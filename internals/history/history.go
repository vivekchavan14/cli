package history

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/omnitrix-sh/cli/internals/database"
	"github.com/omnitrix-sh/cli/internals/pubsub"
)

const (
	InitialVersion = "initial"
)

type File struct {
	ID        string
	SessionID string
	Path      string
	Content   string
	Version   string
	CreatedAt int64
	UpdatedAt int64
}

type Service interface {
	pubsub.Suscriber[File]
	Create(ctx context.Context, sessionID, path, content string) (File, error)
	CreateVersion(ctx context.Context, sessionID, path, content string) (File, error)
	Get(ctx context.Context, id string) (File, error)
	GetByPathAndSession(ctx context.Context, path, sessionID string) (File, error)
	ListBySession(ctx context.Context, sessionID string) ([]File, error)
	ListLatestSessionFiles(ctx context.Context, sessionID string) ([]File, error)
	Update(ctx context.Context, file File) (File, error)
	Delete(ctx context.Context, id string) error
	DeleteSessionFiles(ctx context.Context, sessionID string) error
}

type service struct {
	*pubsub.Broker[File]
	db *sql.DB
	q  *database.Queries
}

func NewService(q *database.Queries, db *sql.DB) Service {
	return &service{
		Broker: pubsub.NewBroker[File](),
		q:      q,
		db:     db,
	}
}

func (s *service) Create(ctx context.Context, sessionID, path, content string) (File, error) {
	return s.createWithVersion(ctx, sessionID, path, content, InitialVersion)
}

func (s *service) CreateVersion(ctx context.Context, sessionID, path, content string) (File, error) {
	// Get the latest version for this path from the database
	// For now, use a simplified approach without querying the DB
	nextVersion := fmt.Sprintf("v%d", time.Now().UnixNano())
	return s.createWithVersion(ctx, sessionID, path, content, nextVersion)
}

func (s *service) createWithVersion(ctx context.Context, sessionID, path, content, version string) (File, error) {
	const maxRetries = 3
	var file File
	var err error

	for attempt := range maxRetries {
		// Start a transaction
		tx, txErr := s.db.BeginTx(ctx, nil)
		if txErr != nil {
			return File{}, fmt.Errorf("failed to begin transaction: %w", txErr)
		}

		// Generate unique ID
		id := uuid.New().String()
		now := time.Now().Unix()

		// Insert file record
		insertSQL := `
			INSERT INTO files (id, session_id, path, content, version, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)
		`
		_, txErr = tx.ExecContext(ctx, insertSQL, id, sessionID, path, content, version, now, now)
		if txErr != nil {
			tx.Rollback()

			// Check if this is a uniqueness constraint violation
			if strings.Contains(txErr.Error(), "UNIQUE constraint failed") {
				if attempt < maxRetries-1 {
					// Try again with a new version
					version = fmt.Sprintf("v%d-%d", time.Now().UnixNano(), attempt)
					continue
				}
			}
			return File{}, txErr
		}

		// Commit the transaction
		if txErr = tx.Commit(); txErr != nil {
			return File{}, fmt.Errorf("failed to commit transaction: %w", txErr)
		}

		file = File{
			ID:        id,
			SessionID: sessionID,
			Path:      path,
			Content:   content,
			Version:   version,
			CreatedAt: now,
			UpdatedAt: now,
		}
		s.Publish(pubsub.CreatedEvent, file)
		return file, nil
	}

	return file, err
}

func (s *service) Get(ctx context.Context, id string) (File, error) {
	query := `
		SELECT id, session_id, path, content, version, created_at, updated_at
		FROM files
		WHERE id = ?
	`
	var f File
	err := s.db.QueryRowContext(ctx, query, id).Scan(
		&f.ID, &f.SessionID, &f.Path, &f.Content, &f.Version, &f.CreatedAt, &f.UpdatedAt,
	)
	if err != nil {
		return File{}, err
	}
	return f, nil
}

func (s *service) GetByPathAndSession(ctx context.Context, path, sessionID string) (File, error) {
	query := `
		SELECT id, session_id, path, content, version, created_at, updated_at
		FROM files
		WHERE path = ? AND session_id = ?
		ORDER BY created_at DESC
		LIMIT 1
	`
	var f File
	err := s.db.QueryRowContext(ctx, query, path, sessionID).Scan(
		&f.ID, &f.SessionID, &f.Path, &f.Content, &f.Version, &f.CreatedAt, &f.UpdatedAt,
	)
	if err != nil {
		return File{}, err
	}
	return f, nil
}

func (s *service) ListBySession(ctx context.Context, sessionID string) ([]File, error) {
	query := `
		SELECT id, session_id, path, content, version, created_at, updated_at
		FROM files
		WHERE session_id = ?
		ORDER BY created_at DESC
	`
	rows, err := s.db.QueryContext(ctx, query, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var files []File
	for rows.Next() {
		var f File
		err := rows.Scan(&f.ID, &f.SessionID, &f.Path, &f.Content, &f.Version, &f.CreatedAt, &f.UpdatedAt)
		if err != nil {
			return nil, err
		}
		files = append(files, f)
	}
	return files, rows.Err()
}

func (s *service) ListLatestSessionFiles(ctx context.Context, sessionID string) ([]File, error) {
	query := `
		SELECT DISTINCT ON (path) id, session_id, path, content, version, created_at, updated_at
		FROM files
		WHERE session_id = ?
		ORDER BY path, created_at DESC
	`
	rows, err := s.db.QueryContext(ctx, query, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var files []File
	pathMap := make(map[string]bool)
	
	query2 := `
		SELECT id, session_id, path, content, version, created_at, updated_at
		FROM files
		WHERE session_id = ?
		ORDER BY path, created_at DESC
	`
	rows, err = s.db.QueryContext(ctx, query2, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var f File
		err := rows.Scan(&f.ID, &f.SessionID, &f.Path, &f.Content, &f.Version, &f.CreatedAt, &f.UpdatedAt)
		if err != nil {
			return nil, err
		}
		if !pathMap[f.Path] {
			files = append(files, f)
			pathMap[f.Path] = true
		}
	}
	return files, rows.Err()
}

func (s *service) Update(ctx context.Context, file File) (File, error) {
	query := `
		UPDATE files
		SET content = ?, updated_at = ?
		WHERE id = ?
	`
	now := time.Now().Unix()
	_, err := s.db.ExecContext(ctx, query, file.Content, now, file.ID)
	if err != nil {
		return File{}, err
	}

	file.UpdatedAt = now
	s.Publish(pubsub.UpdatedEvent, file)
	return file, nil
}

func (s *service) Delete(ctx context.Context, id string) error {
	file, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	
	query := `DELETE FROM files WHERE id = ?`
	_, err = s.db.ExecContext(ctx, query, id)
	if err != nil {
		return err
	}
	
	s.Publish(pubsub.DeletedEvent, file)
	return nil
}

func (s *service) DeleteSessionFiles(ctx context.Context, sessionID string) error {
	files, err := s.ListBySession(ctx, sessionID)
	if err != nil {
		return err
	}
	for _, file := range files {
		err = s.Delete(ctx, file.ID)
		if err != nil {
			return err
		}
	}
	return nil
}
