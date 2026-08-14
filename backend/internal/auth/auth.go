// Package auth turns a request into an Actor: the owner with a session, a
// machine with a token, or a visitor without an account.
//
// The access token is short-lived and lives in the browser's memory. It is
// bound to an httpOnly cookie (`fgp`), so a stolen token on its own is
// useless. Renewal happens through a refresh token, also httpOnly.
package auth

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/offlinebot/home-projects/backend/internal/config"
	"github.com/offlinebot/home-projects/backend/internal/httpx"
	"github.com/offlinebot/home-projects/backend/internal/model"
	"github.com/offlinebot/home-projects/backend/internal/secret"
	"github.com/offlinebot/home-projects/backend/internal/store"
)

const (
	CookieFGP     = "fgp"
	CookieRefresh = "refresh"
	CookieUnlock  = "unlock"

	actorKey = "actor"
)

// Actor is who is asking. Every permission check takes one.
type Actor struct {
	User      *model.User
	SessionID uuid.UUID
	StepUpAt  *time.Time
	Token     *model.Token
	// Unlocked holds the ids of password-protected groups and projects this
	// visitor has already opened.
	Unlocked map[uuid.UUID]bool
	IP       string
}

func (a *Actor) IsUser() bool { return a != nil && a.User != nil }

// IsAdmin is the owner: the account that was here first, and whoever it lets
// in afterwards. An admin sees everything; everyone else sees their own.
func (a *Actor) IsAdmin() bool { return a.IsUser() && a.User.IsOwner }

// Owns reports whether this actor is the one who made a thing.
func (a *Actor) Owns(ownerID uuid.UUID) bool {
	return a.IsUser() && a.User.ID == ownerID
}

func (a *Actor) UserID() *uuid.UUID {
	if a == nil || a.User == nil {
		return nil
	}
	id := a.User.ID
	return &id
}

// HasUnlocked reports whether a password-protected object was opened.
func (a *Actor) HasUnlocked(id uuid.UUID) bool {
	if a == nil {
		return false
	}
	return a.Unlocked[id]
}

// SteppedUp reports whether the password was confirmed recently enough.
func (a *Actor) SteppedUp(within time.Duration) bool {
	if a == nil || a.StepUpAt == nil {
		return false
	}
	return time.Since(*a.StepUpAt) <= within
}

type accessClaims struct {
	jwt.RegisteredClaims
	SessionID string `json:"sid"`
	FGP       string `json:"fgp"`
}

type unlockClaims struct {
	jwt.RegisteredClaims
	Unlocked []string `json:"unlocked"`
}

type Service struct {
	cfg   *config.Config
	store *store.Store
}

func New(cfg *config.Config, st *store.Store) *Service {
	return &Service{cfg: cfg, store: st}
}

var ErrInvalidToken = errors.New("invalid or expired token")

// -------------------------------------------------------------- access token

func (s *Service) signAccess(userID, sessionID uuid.UUID, fgpValue string) (string, error) {
	now := time.Now()
	claims := accessClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID.String(),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(s.cfg.AccessTTL)),
		},
		SessionID: sessionID.String(),
		FGP:       secret.Fingerprint(fgpValue),
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(s.cfg.JWTSecret)
}

func (s *Service) parseAccess(token string) (*accessClaims, error) {
	var claims accessClaims
	_, err := jwt.ParseWithClaims(token, &claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, ErrInvalidToken
		}
		return s.cfg.JWTSecret, nil
	}, jwt.WithValidMethods([]string{"HS256"}))
	if err != nil {
		return nil, ErrInvalidToken
	}
	return &claims, nil
}

// ------------------------------------------------------------------- cookies

func (s *Service) setCookie(c *fiber.Ctx, name, value string, ttl time.Duration, path string) {
	c.Cookie(&fiber.Cookie{
		Name:     name,
		Value:    value,
		Path:     path,
		Domain:   s.cfg.CookieDomain,
		Expires:  time.Now().Add(ttl),
		HTTPOnly: true,
		Secure:   s.cfg.CookieSecure,
		SameSite: "Strict",
	})
}

func (s *Service) clearCookie(c *fiber.Ctx, name, path string) {
	c.Cookie(&fiber.Cookie{
		Name:     name,
		Value:    "",
		Path:     path,
		Domain:   s.cfg.CookieDomain,
		Expires:  time.Now().Add(-time.Hour),
		HTTPOnly: true,
		Secure:   s.cfg.CookieSecure,
		SameSite: "Strict",
	})
}

// ------------------------------------------------------------------- login

type LoginResult struct {
	AccessToken string     `json:"accessToken"`
	ExpiresIn   int        `json:"expiresIn"`
	User        model.User `json:"user"`
}

const (
	maxFailures  = 8
	failureBlock = 15 * time.Minute
)

// Login checks the password and opens a session. Failed attempts are counted
// and throttled.
func (s *Service) Login(ctx context.Context, c *fiber.Ctx, username, password, totpCode string) (*LoginResult, error) {
	ip := ClientIP(c)
	fails, err := s.store.RecentFailures(ctx, "login", strings.ToLower(username), failureBlock)
	if err != nil {
		return nil, httpx.Internal("login could not be checked").WithCause(err)
	}
	if fails >= maxFailures {
		return nil, httpx.TooMany("Too many failed attempts. Try again in %d minutes.", int(failureBlock.Minutes()))
	}

	user, err := s.store.UserByName(ctx, username)
	if err == nil && !user.Approved {
		// Said plainly rather than as "wrong password": the account exists and
		// the answer is "not yet", which is a different thing to be told.
		s.store.RecordAttempt(ctx, "login", strings.ToLower(username), ip, false)
		return nil, httpx.New(fiber.StatusForbidden, "not_approved",
			"This account is waiting to be let in.")
	}
	if err != nil || !secret.Verify(password, user.PasswordHash) {
		s.store.RecordAttempt(ctx, "login", strings.ToLower(username), ip, false)
		s.store.Audit(ctx, nil, "login.failed", username, ip, nil)
		return nil, httpx.Unauthorized("Username or password is wrong.")
	}

	if user.TOTPEnabled {
		if totpCode == "" {
			return nil, httpx.New(fiber.StatusUnauthorized, "totp_required", "This account asks for a second factor.")
		}
		if !VerifyTOTP(user.TOTPSecret, totpCode) {
			s.store.RecordAttempt(ctx, "login", strings.ToLower(username), ip, false)
			return nil, httpx.Unauthorized("The code is not right.")
		}
	}

	s.store.RecordAttempt(ctx, "login", strings.ToLower(username), ip, true)
	return s.openSession(ctx, c, user)
}

func (s *Service) openSession(ctx context.Context, c *fiber.Ctx, user *model.User) (*LoginResult, error) {
	fgp := secret.Token(32)
	refresh := secret.Token(32)
	expires := time.Now().Add(s.cfg.RefreshTTL)

	sess, err := s.store.CreateSession(ctx, user.ID,
		secret.Fingerprint(fgp), secret.Fingerprint(refresh),
		string(c.Request().Header.UserAgent()), ClientIP(c), expires)
	if err != nil {
		return nil, httpx.Internal("session could not be created").WithCause(err)
	}

	access, err := s.signAccess(user.ID, sess.ID, fgp)
	if err != nil {
		return nil, httpx.Internal("token could not be signed").WithCause(err)
	}

	s.setCookie(c, CookieFGP, fgp, s.cfg.RefreshTTL, "/")
	s.setCookie(c, CookieRefresh, sess.ID.String()+"."+refresh, s.cfg.RefreshTTL, "/")
	s.store.Audit(ctx, &user.ID, "login", user.Username, ClientIP(c), nil)

	return &LoginResult{
		AccessToken: access,
		ExpiresIn:   int(s.cfg.AccessTTL.Seconds()),
		User:        *user,
	}, nil
}

// Refresh exchanges the refresh cookie for a new access token and rotates the
// refresh token.
func (s *Service) Refresh(ctx context.Context, c *fiber.Ctx) (*LoginResult, error) {
	raw := c.Cookies(CookieRefresh)
	sid, value, ok := strings.Cut(raw, ".")
	if !ok {
		return nil, httpx.Unauthorized("Not signed in.")
	}
	sessionID, err := uuid.Parse(sid)
	if err != nil {
		return nil, httpx.Unauthorized("Not signed in.")
	}
	sess, fgpHash, refreshHash, err := s.store.SessionAuth(ctx, sessionID)
	if err != nil || !secret.Equal(secret.Fingerprint(value), refreshHash) {
		s.clearCookie(c, CookieRefresh, "/")
		s.clearCookie(c, CookieFGP, "/")
		return nil, httpx.Unauthorized("The session has expired. Please sign in again.")
	}
	// The binding cookie has to match too, otherwise the refresh token alone
	// would be enough.
	if !secret.Equal(secret.Fingerprint(c.Cookies(CookieFGP)), fgpHash) {
		return nil, httpx.Unauthorized("This session belongs to another device.")
	}

	user, err := s.store.UserByID(ctx, sess.UserID)
	if err != nil {
		return nil, httpx.Unauthorized("The account no longer exists.")
	}

	newRefresh := secret.Token(32)
	expires := time.Now().Add(s.cfg.RefreshTTL)
	if err := s.store.RotateRefresh(ctx, sess.ID, secret.Fingerprint(newRefresh), expires); err != nil {
		return nil, httpx.Internal("session could not be renewed").WithCause(err)
	}
	s.setCookie(c, CookieRefresh, sess.ID.String()+"."+newRefresh, s.cfg.RefreshTTL, "/")

	access, err := s.signAccess(user.ID, sess.ID, c.Cookies(CookieFGP))
	if err != nil {
		return nil, httpx.Internal("token could not be signed").WithCause(err)
	}
	return &LoginResult{AccessToken: access, ExpiresIn: int(s.cfg.AccessTTL.Seconds()), User: *user}, nil
}

func (s *Service) Logout(ctx context.Context, c *fiber.Ctx, actor *Actor) {
	if actor != nil && actor.SessionID != uuid.Nil && actor.User != nil {
		_ = s.store.RevokeSession(ctx, actor.User.ID, actor.SessionID)
		s.store.Audit(ctx, actor.UserID(), "logout", actor.User.Username, ClientIP(c), nil)
	}
	s.clearCookie(c, CookieRefresh, "/")
	s.clearCookie(c, CookieFGP, "/")
}

// StepUp confirms the password again inside an open session.
func (s *Service) StepUp(ctx context.Context, c *fiber.Ctx, actor *Actor, password string) error {
	if !actor.IsUser() {
		return httpx.Unauthorized("Not signed in.")
	}
	ip := ClientIP(c)
	fails, _ := s.store.RecentFailures(ctx, "stepup", actor.User.ID.String(), failureBlock)
	if fails >= maxFailures {
		return httpx.TooMany("Too many failed attempts. Try again later.")
	}
	if !secret.Verify(password, actor.User.PasswordHash) {
		s.store.RecordAttempt(ctx, "stepup", actor.User.ID.String(), ip, false)
		return httpx.Unauthorized("The password is not right.")
	}
	s.store.RecordAttempt(ctx, "stepup", actor.User.ID.String(), ip, true)
	if err := s.store.MarkStepUp(ctx, actor.SessionID); err != nil {
		return httpx.Internal("step-up could not be stored").WithCause(err)
	}
	now := time.Now()
	actor.StepUpAt = &now
	return nil
}

// RequireStepUp is the guard in front of everything sensitive.
func (s *Service) RequireStepUp(actor *Actor, action string) error {
	if !actor.IsUser() {
		return httpx.Unauthorized("Not signed in.")
	}
	if !actor.SteppedUp(s.cfg.StepUpTTL) {
		return httpx.StepUpRequired(action)
	}
	return nil
}

// ------------------------------------------------------------- unlock cookie

// Unlock notes that a visitor entered the password of a group or project.
func (s *Service) Unlock(c *fiber.Ctx, actor *Actor, id uuid.UUID) error {
	ids := []string{id.String()}
	for existing := range actor.Unlocked {
		if existing != id {
			ids = append(ids, existing.String())
		}
	}
	claims := unlockClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(12 * time.Hour)),
		},
		Unlocked: ids,
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(s.cfg.JWTSecret)
	if err != nil {
		return httpx.Internal("unlock could not be stored").WithCause(err)
	}
	s.setCookie(c, CookieUnlock, signed, 12*time.Hour, "/")
	if actor.Unlocked == nil {
		actor.Unlocked = map[uuid.UUID]bool{}
	}
	actor.Unlocked[id] = true
	return nil
}

func (s *Service) readUnlocked(c *fiber.Ctx) map[uuid.UUID]bool {
	out := map[uuid.UUID]bool{}
	raw := c.Cookies(CookieUnlock)
	if raw == "" {
		return out
	}
	var claims unlockClaims
	_, err := jwt.ParseWithClaims(raw, &claims, func(t *jwt.Token) (any, error) {
		return s.cfg.JWTSecret, nil
	}, jwt.WithValidMethods([]string{"HS256"}))
	if err != nil {
		return out
	}
	for _, id := range claims.Unlocked {
		if u, err := uuid.Parse(id); err == nil {
			out[u] = true
		}
	}
	return out
}

// ---------------------------------------------------------------- middleware

// Attach resolves the actor for every request. It never rejects: a request
// without credentials is simply a visitor.
func (s *Service) Attach() fiber.Handler {
	return func(c *fiber.Ctx) error {
		actor := &Actor{Unlocked: s.readUnlocked(c), IP: ClientIP(c)}
		ctx := c.UserContext()

		if bearer := bearerToken(c); bearer != "" {
			if claims, err := s.parseAccess(bearer); err == nil {
				if s.bindingMatches(c, claims) {
					if sessionID, err := uuid.Parse(claims.SessionID); err == nil {
						if sess, _, _, err := s.store.SessionAuth(ctx, sessionID); err == nil {
							if user, err := s.store.UserByID(ctx, sess.UserID); err == nil {
								actor.User = user
								actor.SessionID = sess.ID
								actor.StepUpAt = sess.StepUpAt
								s.store.TouchSession(ctx, sess.ID)
							}
						}
					}
				}
			} else if tok, _, err := s.store.TokenByHash(ctx, secret.Fingerprint(bearer)); err == nil {
				actor.Token = tok
			}
		}
		c.Locals(actorKey, actor)
		return c.Next()
	}
}

// bindingMatches compares the fgp cookie against the token's binding. Without
// the cookie the token is worthless.
func (s *Service) bindingMatches(c *fiber.Ctx, claims *accessClaims) bool {
	cookie := c.Cookies(CookieFGP)
	if cookie == "" || claims.FGP == "" {
		return false
	}
	return secret.Equal(secret.Fingerprint(cookie), claims.FGP)
}

func bearerToken(c *fiber.Ctx) string {
	h := c.Get("Authorization")
	if strings.HasPrefix(h, "Bearer ") {
		return strings.TrimSpace(h[7:])
	}
	// Machines that cannot set a header (calendar subscriptions) may pass it
	// as a query parameter.
	return c.Query("token")
}

// From returns the actor the middleware attached.
func From(c *fiber.Ctx) *Actor {
	if a, ok := c.Locals(actorKey).(*Actor); ok {
		return a
	}
	return &Actor{}
}

// RequireUser is the guard for everything that only the owner may do.
func RequireUser(c *fiber.Ctx) error {
	if !From(c).IsUser() {
		return httpx.Unauthorized("Please sign in.")
	}
	return c.Next()
}

// ClientIP prefers the address nginx forwards.
func ClientIP(c *fiber.Ctx) string {
	if xff := c.Get("X-Forwarded-For"); xff != "" {
		if i := strings.IndexByte(xff, ','); i > 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	return c.IP()
}
