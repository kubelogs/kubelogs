package auth

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"time"
)

// SessionStore manages session persistence.
type SessionStore struct {
	db       *sql.DB
	duration time.Duration
}

// NewSessionStore creates a SessionStore.
func NewSessionStore(db *sql.DB, duration time.Duration) *SessionStore {
	return &SessionStore{db: db, duration: duration}
}

// Create creates a new session for the given user.
func (s *SessionStore) Create(ctx context.Context, userID int64) (*Session, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return nil, err
	}
	sessionID := hex.EncodeToString(bytes)

	now := time.Now()
	expiresAt := now.Add(s.duration)

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO sessions (id, user_id, created_at, expires_at) VALUES (?, ?, ?, ?)`,
		sessionID, userID, now.UnixNano(), expiresAt.UnixNano(),
	)
	if err != nil {
		return nil, err
	}

	return &Session{
		ID:        sessionID,
		UserID:    userID,
		CreatedAt: now,
		ExpiresAt: expiresAt,
	}, nil
}

// Get retrieves a session by ID, returns error if expired.
func (s *SessionStore) Get(ctx context.Context, sessionID string) (*Session, error) {
	var session Session
	var createdAt, expiresAt int64

	err := s.db.QueryRowContext(ctx,
		`SELECT id, user_id, created_at, expires_at FROM sessions WHERE id = ?`,
		sessionID,
	).Scan(&session.ID, &session.UserID, &createdAt, &expiresAt)

	if err == sql.ErrNoRows {
		return nil, ErrSessionNotFound
	}
	if err != nil {
		return nil, err
	}

	session.CreatedAt = time.Unix(0, createdAt)
	session.ExpiresAt = time.Unix(0, expiresAt)

	if time.Now().After(session.ExpiresAt) {
		s.Delete(ctx, sessionID)
		return nil, ErrSessionExpired
	}

	return &session, nil
}

// Delete removes a session.
func (s *SessionStore) Delete(ctx context.Context, sessionID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, sessionID)
	return err
}

// DeleteExpired removes all expired sessions.
func (s *SessionStore) DeleteExpired(ctx context.Context) (int64, error) {
	result, err := s.db.ExecContext(ctx,
		`DELETE FROM sessions WHERE expires_at < ?`,
		time.Now().UnixNano(),
	)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// DeleteByUserID removes all sessions for a user.
func (s *SessionStore) DeleteByUserID(ctx context.Context, userID int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, userID)
	return err
}
