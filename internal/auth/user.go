package auth

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// UserStore manages user persistence.
type UserStore struct {
	db *sql.DB
}

// NewUserStore creates a UserStore with the given database connection.
func NewUserStore(db *sql.DB) *UserStore {
	return &UserStore{db: db}
}

// CreateUser creates a new user with bcrypt-hashed password.
func (s *UserStore) CreateUser(ctx context.Context, username, password string) (*User, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	nowNano := now.UnixNano()
	result, err := s.db.ExecContext(ctx,
		`INSERT INTO users (username, password, created_at, updated_at) VALUES (?, ?, ?, ?)`,
		username, string(hash), nowNano, nowNano,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrUserExists
		}
		return nil, err
	}

	id, _ := result.LastInsertId()
	return &User{
		ID:        id,
		Username:  username,
		CreatedAt: now,
		UpdatedAt: now,
	}, nil
}

// Authenticate verifies credentials and returns the user.
func (s *UserStore) Authenticate(ctx context.Context, username, password string) (*User, error) {
	var user User
	var hash string
	var createdAt, updatedAt int64

	err := s.db.QueryRowContext(ctx,
		`SELECT id, username, password, created_at, updated_at FROM users WHERE username = ?`,
		username,
	).Scan(&user.ID, &user.Username, &hash, &createdAt, &updatedAt)

	if err == sql.ErrNoRows {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, err
	}

	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)); err != nil {
		return nil, ErrInvalidPassword
	}

	user.CreatedAt = time.Unix(0, createdAt)
	user.UpdatedAt = time.Unix(0, updatedAt)
	return &user, nil
}

// GetByID retrieves a user by ID.
func (s *UserStore) GetByID(ctx context.Context, id int64) (*User, error) {
	var user User
	var createdAt, updatedAt int64

	err := s.db.QueryRowContext(ctx,
		`SELECT id, username, created_at, updated_at FROM users WHERE id = ?`,
		id,
	).Scan(&user.ID, &user.Username, &createdAt, &updatedAt)

	if err == sql.ErrNoRows {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, err
	}

	user.CreatedAt = time.Unix(0, createdAt)
	user.UpdatedAt = time.Unix(0, updatedAt)
	return &user, nil
}

// HasUsers returns true if any users exist.
func (s *UserStore) HasUsers(ctx context.Context) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&count)
	return count > 0, err
}

// isUniqueViolation checks if the error is a SQLite unique constraint violation.
func isUniqueViolation(err error) bool {
	return strings.Contains(err.Error(), "UNIQUE constraint failed")
}
