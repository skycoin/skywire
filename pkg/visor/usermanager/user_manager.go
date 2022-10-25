// Package usermanager pkg/visor/usermanager/user_manager.go
package usermanager

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/securecookie"

	"github.com/skycoin/skywire-utilities/pkg/httputil"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/visor/hypervisorconfig"
)

const (
	sessionCookieName = "swm-session"
)

// Errors associated with user management.
var (
	ErrNotLoggedIn       = errors.New("not logged in")
	ErrNotLoggedOut      = errors.New("not logged out")
	ErrBadLogin          = errors.New("incorrect username or password")
	ErrBadSession        = errors.New("session cookie is either non-existent, expired, or ill-formatted")
	ErrMalformedRequest  = errors.New("request format is malformed")
	ErrBadUsernameFormat = errors.New("format of 'username' is not accepted")
	ErrUserNotFound      = errors.New("user is either deleted or not found")
)

// for use with context.Context
type ctxKey string

// cookie constants
const (
	userKey    = ctxKey("user")
	sessionKey = ctxKey("session")
)

// Session represents a user session.
type Session struct {
	SID    uuid.UUID `json:"sid"`
	User   string    `json:"username"`
	Expiry time.Time `json:"expiry"`
}

// UserManager manages the users and sessions.
type UserManager struct {
	log      *logging.Logger
	c        hypervisorconfig.CookieConfig
	db       UserStore
	sessions map[uuid.UUID]Session
	crypto   *securecookie.SecureCookie
	mu       *sync.RWMutex
}

// NewUserManager creates a new UserManager.
func NewUserManager(mLog *logging.MasterLogger, users UserStore, config hypervisorconfig.CookieConfig) *UserManager {
	return &UserManager{
		log:      mLog.PackageLogger("user_manager"),
		db:       users,
		c:        config,
		sessions: make(map[uuid.UUID]Session),
		crypto:   securecookie.New(config.HashKey, config.BlockKey),
		mu:       new(sync.RWMutex),
	}
}

// Login returns a HandlerFunc for login operations.
func (s *UserManager) Login() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, _, ok := s.session(r); ok {
			httputil.WriteJSON(w, r, http.StatusForbidden, ErrNotLoggedOut)
			return
		}

		var rb struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}

		if err := httputil.ReadJSON(r, &rb); err != nil {
			if err != io.EOF {
				s.log.Warnf("Login request: %v", err)
			}

			httputil.WriteJSON(w, r, http.StatusBadRequest, ErrMalformedRequest)

			return
		}

		if !checkUsernameFormat(rb.Username) {
			httputil.WriteJSON(w, r, http.StatusBadRequest, ErrBadUsernameFormat)
			return
		}

		user, err := s.db.User(rb.Username)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			s.log.WithError(err).Errorf("Failed to get user %q", rb.Username)

			return
		}

		if user == nil || !user.VerifyPassword(rb.Password) {
			httputil.WriteJSON(w, r, http.StatusUnauthorized, ErrBadLogin)
			return
		}

		session := Session{
			User:   rb.Username,
			Expiry: time.Now().Add(s.c.ExpiresDuration),
		}

		if err := s.newSession(w, session); err != nil {
			s.log.WithError(err).Errorf("Failed to create a new session")
			w.WriteHeader(http.StatusInternalServerError)

			return
		}

		// http.SetCookie()
		httputil.WriteJSON(w, r, http.StatusOK, true)
	}
}

// Logout returns a HandlerFunc of logout operations.
func (s *UserManager) Logout() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := s.delSession(w, r); err != nil {
			httputil.WriteJSON(w, r, http.StatusBadRequest, ErrNotLoggedIn)
			return
		}

		httputil.WriteJSON(w, r, http.StatusOK, true)
	}
}

// Authorize is an http middleware for authorizing requests.
func (s *UserManager) Authorize(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, session, ok := s.session(r)
		if !ok {
			httputil.WriteJSON(w, r, http.StatusUnauthorized, ErrBadSession)
			return
		}

		ctx := r.Context()
		ctx = context.WithValue(ctx, userKey, user)
		ctx = context.WithValue(ctx, sessionKey, session)

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// ChangePassword returns a HandlerFunc for changing the user's password.
func (s *UserManager) ChangePassword() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var rb struct {
			OldPassword string `json:"old_password"`
			NewPassword string `json:"new_password"`
		}

		if err := httputil.ReadJSON(r, &rb); err != nil {
			if err != io.EOF {
				s.log.Warnf("ChangePassword request: %v", err)
			}

			httputil.WriteJSON(w, r, http.StatusBadRequest, ErrMalformedRequest)

			return
		}

		user := r.Context().Value(userKey).(User)
		if ok := user.VerifyPassword(rb.OldPassword); !ok {
			httputil.WriteJSON(w, r, http.StatusUnauthorized, ErrBadLogin)
			return
		}

		if err := user.SetPassword(rb.NewPassword); err != nil {
			httputil.WriteJSON(w, r, http.StatusBadRequest, err)
			return
		}

		if err := s.db.SetUser(user); err != nil {
			s.log.WithError(err).Errorf("Failed to update user %q data", user.Name)
			w.WriteHeader(http.StatusInternalServerError)

			return
		}

		s.delAllSessionsOfUser(user.Name)
		httputil.WriteJSON(w, r, http.StatusOK, true)
	}
}

// CreateAccount returns a HandlerFunc for account creation.
func (s *UserManager) CreateAccount() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var rb struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}

		if err := httputil.ReadJSON(r, &rb); err != nil {
			if err != io.EOF {
				s.log.Warnf("CreateAccount request: %v", err)
			}

			httputil.WriteJSON(w, r, http.StatusBadRequest, ErrMalformedRequest)

			return
		}

		var user User
		if ok := user.SetName(rb.Username); !ok {
			httputil.WriteJSON(w, r, http.StatusBadRequest, ErrBadUsernameFormat)
			return
		}

		if err := user.SetPassword(rb.Password); err != nil {
			httputil.WriteJSON(w, r, http.StatusBadRequest, err)
			return
		}

		if err := s.db.AddUser(user); err != nil {
			if err == ErrNameNotAllowed {
				httputil.WriteJSON(w, r, http.StatusForbidden, ErrNameNotAllowed)
				return
			}

			s.log.WithError(err).Errorf("Failed to create user %q account", user.Name)
			w.WriteHeader(http.StatusInternalServerError)

			return
		}

		httputil.WriteJSON(w, r, http.StatusOK, true)
	}
}

// UserInfo returns a HandlerFunc for obtaining user info.
func (s *UserManager) UserInfo() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var (
			user    User
			session Session
		)

		userIfc := r.Context().Value(userKey)
		if userIfc != nil {
			user = userIfc.(User)
		}

		sessionIfc := r.Context().Value(sessionKey)
		if sessionIfc != nil {
			session = sessionIfc.(Session)
		}

		var otherSessions []Session

		s.mu.RLock()

		for _, s := range s.sessions {
			if s.User == user.Name && s.SID != session.SID {
				otherSessions = append(otherSessions, s)
			}
		}

		s.mu.RUnlock()

		resp := struct {
			Username string    `json:"username"`
			Current  Session   `json:"current_session"`
			Sessions []Session `json:"other_sessions"`
		}{
			Username: user.Name,
			Current:  session,
			Sessions: otherSessions,
		}

		httputil.WriteJSON(w, r, http.StatusOK, resp)
	}
}

func (s *UserManager) newSession(w http.ResponseWriter, session Session) error {
	session.SID = uuid.New()

	s.mu.Lock()
	s.sessions[session.SID] = session
	s.mu.Unlock()

	value, err := s.crypto.Encode(sessionCookieName, session.SID)
	if err != nil {
		return fmt.Errorf("encode SID cookie: %w", err)
	}

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    value,
		Path:     s.c.Path,
		Domain:   s.c.Domain,
		Expires:  time.Now().Add(s.c.ExpiresDuration),
		Secure:   s.c.Secure(),
		HttpOnly: s.c.HTTPOnly(),
		SameSite: s.c.SameSite(),
	})

	return nil
}

// Close closes the underlying db, used for Windows.
func (s *UserManager) Close() error {
	return s.db.Close()
}

func (s *UserManager) delSession(w http.ResponseWriter, r *http.Request) error {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return err
	}

	var sid uuid.UUID
	if err := s.crypto.Decode(sessionCookieName, cookie.Value, &sid); err != nil {
		return err
	}

	s.mu.Lock()
	delete(s.sessions, sid)
	s.mu.Unlock()

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Path:     s.c.Path,
		Domain:   s.c.Domain,
		MaxAge:   -1,
		Secure:   s.c.Secure(),
		HttpOnly: s.c.HTTPOnly(),
		SameSite: s.c.SameSite(),
	})

	return nil
}

func (s *UserManager) delAllSessionsOfUser(userName string) {
	s.mu.Lock()

	for sid, session := range s.sessions {
		if session.User == userName {
			delete(s.sessions, sid)
		}
	}

	s.mu.Unlock()
}

func (s *UserManager) session(r *http.Request) (User, Session, bool) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return User{}, Session{}, false
	}

	var sid uuid.UUID
	if err := s.crypto.Decode(sessionCookieName, cookie.Value, &sid); err != nil {
		s.log.WithError(err).Warn("Failed to decode session cookie value")
		return User{}, Session{}, false
	}

	s.mu.RLock()
	session, ok := s.sessions[sid]
	s.mu.RUnlock()

	if !ok {
		return User{}, Session{}, false
	}

	user, err := s.db.User(session.User)
	if err != nil {
		s.log.WithError(err).Errorf("Failed to fetch user %q data", user.Name)
		return User{}, Session{}, false
	}

	if user == nil {
		return User{}, Session{}, false
	}

	if time.Now().After(session.Expiry) {
		s.mu.Lock()
		delete(s.sessions, sid)
		s.mu.Unlock()

		return User{}, Session{}, false
	}

	return *user, session, true
}
