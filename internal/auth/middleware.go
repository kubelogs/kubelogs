package auth

import (
	"net/http"
)

// Middleware provides authentication middleware.
type Middleware struct {
	userStore    *UserStore
	sessionStore *SessionStore
	cookieName   string
	cookieSecure bool
}

// NewMiddleware creates auth middleware.
func NewMiddleware(users *UserStore, sessions *SessionStore, cookieName string, secure bool) *Middleware {
	return &Middleware{
		userStore:    users,
		sessionStore: sessions,
		cookieName:   cookieName,
		cookieSecure: secure,
	}
}

// CookieName returns the session cookie name.
func (m *Middleware) CookieName() string {
	return m.cookieName
}

// RequireAuth wraps a handler to require authentication.
// Redirects unauthenticated requests to /login.
func (m *Middleware) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(m.cookieName)
		if err != nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		session, err := m.sessionStore.Get(r.Context(), cookie.Value)
		if err != nil {
			m.clearCookie(w)
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		user, err := m.userStore.GetByID(r.Context(), session.UserID)
		if err != nil {
			m.clearCookie(w)
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		ctx := ContextWithUser(r.Context(), user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequireAuthAPI wraps an API handler to require authentication.
// Returns 401 Unauthorized instead of redirecting.
func (m *Middleware) RequireAuthAPI(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(m.cookieName)
		if err != nil {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		session, err := m.sessionStore.Get(r.Context(), cookie.Value)
		if err != nil {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		user, err := m.userStore.GetByID(r.Context(), session.UserID)
		if err != nil {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		ctx := ContextWithUser(r.Context(), user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// SetSessionCookie sets the session cookie.
func (m *Middleware) SetSessionCookie(w http.ResponseWriter, sessionID string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name:     m.cookieName,
		Value:    sessionID,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   m.cookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
}

func (m *Middleware) clearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     m.cookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
	})
}
