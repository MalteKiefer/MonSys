// Package api: profile.go implements self-service profile endpoints that
// extend /v1/auth/me — avatar upload/fetch/delete and the two halves of the
// verified email-change flow.
//
// Avatar bytes live as a BYTEA on the users row (migration 0031). The fetch
// endpoint streams raw bytes and therefore bypasses huma — see
// handleGetUserAvatar.
//
// Email change is a two-step flow over the existing user_action_tokens
// machinery: step 1 mails a one-hour confirmation link to the NEW address
// (so it proves control); step 2 consumes that token, rewrites users.email,
// and revokes every session for the user.
package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"image"

	// audit 2026-05-12 F-4: decoder registrations for the avatar allow-list.
	// The blank imports register handlers with image.Decode so we can verify
	// the bytes match the claimed content-type instead of trusting the
	// client.
	_ "image/jpeg"
	_ "image/png"
	"net/http"
	"strings"
	"time"

	_ "golang.org/x/image/webp"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/MalteKiefer/MonSys/internal/server/store"
	"github.com/MalteKiefer/MonSys/internal/shared/apitypes"
)

// --- Avatar ---------------------------------------------------------------

type setAvatarInput struct {
	Body apitypes.AvatarUploadRequest
}

// validAvatarContentType is the small allow-list the frontend re-encodes
// into. Keeps surface tiny — anything outside this is a configuration bug
// at the client, not user input we need a helpful 4xx message for.
func validAvatarContentType(ct string) bool {
	switch ct {
	case "image/png", "image/jpeg", "image/webp":
		return true
	}
	return false
}

func (s *Server) handleSetAvatar(ctx context.Context, in *setAvatarInput) (*emptyOutput, error) {
	u, ok := userFromContext(ctx)
	if !ok {
		return nil, huma.Error401Unauthorized("no session")
	}
	if !validAvatarContentType(in.Body.ContentType) {
		return nil, huma.Error400BadRequest("content_type must be image/png, image/jpeg, or image/webp")
	}
	raw, err := base64.StdEncoding.DecodeString(in.Body.DataB64)
	if err != nil {
		// Some clients use the URL-safe variant; accept both.
		raw, err = base64.RawStdEncoding.DecodeString(in.Body.DataB64)
		if err != nil {
			return nil, huma.Error400BadRequest("data_b64 must be valid base64")
		}
	}
	if len(raw) == 0 {
		return nil, huma.Error400BadRequest("data_b64 decoded empty")
	}
	if len(raw) > store.MaxAvatarBytes {
		return nil, huma.Error413RequestEntityTooLarge("avatar exceeds size limit")
	}
	// audit 2026-05-12 F-4: verify the bytes actually decode as one of the
	// allow-listed formats and that the detected format matches what the
	// client claimed. We store the DETECTED content-type — the client's
	// `content_type` field is only consulted for the initial allow-list
	// check above; trusting it past that would let a hostile client serve
	// arbitrary bytes under an image/* MIME and rely on browser sniffing.
	_, format, decodeErr := image.Decode(bytes.NewReader(raw))
	if decodeErr != nil {
		return nil, huma.Error400BadRequest("avatar bytes are not a valid image")
	}
	switch format {
	case "png", "jpeg", "webp":
		// ok
	default:
		return nil, huma.Error400BadRequest("avatar format must be png, jpeg, or webp")
	}
	detectedCT := "image/" + format
	if detectedCT != in.Body.ContentType {
		return nil, huma.Error400BadRequest("content_type does not match decoded image bytes")
	}
	if err := s.Store.SetAvatar(ctx, u.ID, detectedCT, raw); err != nil {
		if errors.Is(err, store.ErrAvatarTooBig) {
			return nil, huma.Error413RequestEntityTooLarge("avatar exceeds size limit")
		}
		if errors.Is(err, store.ErrUserNotFound) {
			return nil, huma.Error404NotFound("user not found")
		}
		return nil, internalErr(ctx, "set avatar failed", err)
	}
	s.audit(ctx, "user.avatar.set", u.ID.String(), detectedCT)
	out := &emptyOutput{}
	out.Body.OK = true
	return out, nil
}

func (s *Server) handleDeleteAvatar(ctx context.Context, _ *emptyInput) (*emptyOutput, error) {
	u, ok := userFromContext(ctx)
	if !ok {
		return nil, huma.Error401Unauthorized("no session")
	}
	if err := s.Store.DeleteAvatar(ctx, u.ID); err != nil {
		return nil, internalErr(ctx, "delete avatar failed", err)
	}
	s.audit(ctx, "user.avatar.delete", u.ID.String(), "")
	out := &emptyOutput{}
	out.Body.OK = true
	return out, nil
}

// handleGetUserAvatar streams the raw avatar bytes for a user. It bypasses
// huma because huma's content negotiation always wraps responses in a typed
// envelope; image bytes need to come out as the original Content-Type.
//
// Authentication is enforced inline. We accept either a bearer session token
// in the Authorization header OR — to keep the SPA's <img src> simple — no
// header at all is fine ONLY because chi-level rate limiting + the inherent
// information density (a 200x200 PNG of someone's face) make this barely a
// privacy concern. To stay consistent with the rest of the API surface,
// however, we still require a valid session so the avatar endpoint matches
// /v1/auth/me. The login-pending user (force-mode interstitial) is allowed:
// requireUser-level checks only, no compliance gate.
func (s *Server) handleGetUserAvatar(w http.ResponseWriter, r *http.Request) {
	if s.Store == nil {
		http.NotFound(w, r)
		return
	}

	tok, ok := bearer(r.Header.Get("Authorization"))
	if !ok {
		http.Error(w, "missing session token", http.StatusUnauthorized)
		return
	}
	if _, err := s.Store.ValidateSession(r.Context(), tok); err != nil {
		http.Error(w, "invalid session", http.StatusUnauthorized)
		return
	}

	rawID := strings.TrimSpace(chi.URLParam(r, "id"))
	id, err := uuid.Parse(rawID)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	b, ct, err := s.Store.GetAvatar(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrAvatarNotFound) || errors.Is(err, store.ErrUserNotFound) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", ct)
	// audit 2026-05-12 F-4: defense-in-depth against MIME sniffing. The
	// upload path already verifies bytes match the stored content-type, but
	// nosniff stops a browser from second-guessing us if anything ever
	// regresses there.
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// Short cache so the SPA doesn't refetch on every render. The
	// avatar_updated_at column gives clients a cheap cache-buster.
	w.Header().Set("Cache-Control", "private, max-age=300")
	_, _ = w.Write(b)
}

// --- Email change ---------------------------------------------------------

type requestEmailChangeInput struct {
	Body apitypes.RequestEmailChangeRequest
}

func (s *Server) handleRequestEmailChange(ctx context.Context, in *requestEmailChangeInput) (*emptyOutput, error) {
	u, ok := userFromContext(ctx)
	if !ok {
		return nil, huma.Error401Unauthorized("no session")
	}
	if in.Body.NewEmail == "" {
		return nil, huma.Error400BadRequest("new_email required")
	}
	if in.Body.CurrentPassword == "" {
		return nil, huma.Error400BadRequest("current_password required")
	}
	// audit 2026-05-12 F-11: per-user throttle. 60 s minimum between
	// successive email-change requests so a single session cannot spam the
	// new-address inbox with confirmation links. The map is also opportun-
	// istically swept of entries older than an hour so it stays bounded.
	if !s.emailChangeAllowed(u.ID) {
		return nil, huma.Error429TooManyRequests("email change request rate limited; try again in a moment")
	}
	// Reject no-op changes early to give a friendly error rather than a
	// confusing "email already in use" later on.
	if strings.EqualFold(strings.TrimSpace(in.Body.NewEmail), strings.TrimSpace(u.Email)) {
		return nil, huma.Error400BadRequest("new_email matches current email")
	}
	// AuthenticateUser is the existing primitive that verifies the current
	// password. We deliberately re-use it (instead of a fresh bcrypt query)
	// so AUDIT-013 lockout counters apply uniformly to password verifies
	// from every entry point.
	if _, err := s.Store.AuthenticateUser(ctx, u.Email, in.Body.CurrentPassword); err != nil {
		return nil, huma.Error401Unauthorized("current password is wrong")
	}
	tok, err := s.Store.RequestEmailChange(ctx, u.ID, in.Body.NewEmail, u.Email)
	if err != nil {
		if errors.Is(err, store.ErrUserExists) {
			return nil, huma.Error409Conflict("email already in use")
		}
		return nil, internalErr(ctx, "request email change failed", err)
	}
	// Mail the token URL to the NEW address. Best-effort: if SMTP is
	// unconfigured we still record the audit so the operator can hand-deliver.
	// We deliberately do NOT include the token in the API response so a
	// session-hijacked attacker cannot harvest it without inbox access.
	url := emailConfirmURL(tok)
	if err := s.sendInviteMail(ctx, in.Body.NewEmail, url); err != nil {
		// Non-fatal: log it via audit and let the caller retry. The user
		// will still see the "we sent a link" UI; an operator who is
		// monitoring audit will spot the failure and intervene.
		s.audit(ctx, "user.email.change_requested", u.ID.String(),
			"mail_failed: "+in.Body.NewEmail+": "+err.Error())
	} else {
		s.audit(ctx, "user.email.change_requested", u.ID.String(), in.Body.NewEmail)
	}
	out := &emptyOutput{}
	out.Body.OK = true
	return out, nil
}

type confirmEmailChangeInput struct {
	Body apitypes.ConfirmEmailChangeRequest
}

func (s *Server) handleConfirmEmailChange(ctx context.Context, in *confirmEmailChangeInput) (*emptyOutput, error) {
	if in.Body.Token == "" {
		return nil, huma.Error400BadRequest("token required")
	}
	u, err := s.Store.ConsumeEmailChange(ctx, in.Body.Token)
	if err != nil {
		if errors.Is(err, store.ErrActionTokenInvalid) {
			return nil, huma.Error401Unauthorized("token invalid or expired")
		}
		if errors.Is(err, store.ErrUserExists) {
			return nil, huma.Error409Conflict("email already in use")
		}
		return nil, internalErr(ctx, "confirm email change failed", err)
	}
	s.audit(ctx, "user.email.change_confirmed", u.ID.String(), u.Email)
	out := &emptyOutput{}
	out.Body.OK = true
	return out, nil
}

// emailConfirmURL builds the SPA path that consumes an email-change token.
// Mirrors inviteURL but routes to /email-confirm instead of /reset.
func emailConfirmURL(token string) string { return "/email-confirm?token=" + token }

// emailChangeAllowed enforces the 60 s per-user cooldown on
// /v1/auth/email/request. Returns true and records `now` when the caller is
// past the cooldown; returns false otherwise. Each call also opportunis-
// tically purges entries older than an hour so the map stays bounded.
// audit 2026-05-12 F-11.
func (s *Server) emailChangeAllowed(userID uuid.UUID) bool {
	const cooldown = 60 * time.Second
	const purgeAge = time.Hour
	s.emailChangeThrottleMu.Lock()
	defer s.emailChangeThrottleMu.Unlock()
	now := time.Now()
	if s.emailChangeThrottle == nil {
		s.emailChangeThrottle = make(map[uuid.UUID]time.Time)
	}
	for uid, ts := range s.emailChangeThrottle {
		if now.Sub(ts) > purgeAge {
			delete(s.emailChangeThrottle, uid)
		}
	}
	if last, ok := s.emailChangeThrottle[userID]; ok && now.Sub(last) < cooldown {
		return false
	}
	s.emailChangeThrottle[userID] = now
	return true
}
