package auth

import (
	"context"
	"errors"
	"time"
)

var (
	ErrUserNotFound    = errors.New("auth: user not found")
	ErrUserExists      = errors.New("auth: user already exists")
	ErrInvalidPassword = errors.New("auth: invalid password")
	ErrSessionNotFound = errors.New("auth: session not found")
	ErrSessionExpired  = errors.New("auth: session expired")
)

// User represents an authenticated user.
type User struct {
	ID        int64
	Username  string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Session represents an authenticated session.
type Session struct {
	ID        string
	UserID    int64
	CreatedAt time.Time
	ExpiresAt time.Time
}

// contextKey is used for storing user in context.
type contextKey int

const userContextKey contextKey = iota

// UserFromContext retrieves the authenticated user from context.
func UserFromContext(ctx context.Context) (*User, bool) {
	u, ok := ctx.Value(userContextKey).(*User)
	return u, ok
}

// ContextWithUser adds a user to the context.
func ContextWithUser(ctx context.Context, u *User) context.Context {
	return context.WithValue(ctx, userContextKey, u)
}
