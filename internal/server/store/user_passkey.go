// Package store — passkey / WebAuthn methods.
//
// This file owns every Store method that touches `user_credentials` or the
// WebAuthn-handle column on `users`. The crypto live in
// internal/server/webauthn (the RP wrapper); we restrict ourselves to:
//
//  1. Generating + persisting the opaque per-user `webauthn_handle` (W3C
//     §14.6 forbids PII in the WebAuthn user.id).
//  2. Marshalling the library's SessionData between the Begin/Finish halves
//     of a ceremony — for registration we reuse `user_action_tokens` (a
//     known user_id), for discoverable login the user is unknown at Begin
//     so we keep an in-memory map keyed by a random token (action_tokens
//     requires NOT NULL user_id; we don't want to spend a schema change
//     here).
//  3. CRUD on the credential row + audit-log entries.
//
// Anything that needs an *http.Request lives in the API layer; the store
// always receives pre-parsed bytes so it stays HTTP-free.
package store

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/MalteKiefer/MonSys/internal/server/webauthn"
	"github.com/MalteKiefer/MonSys/internal/shared/apitypes"
	"github.com/go-webauthn/webauthn/protocol"
	libwa "github.com/go-webauthn/webauthn/webauthn"
)

// ErrPasskeyNotConfigured is returned by every passkey method when the
// Store's Webauthn service is nil (env vars missing at startup). The API
// layer maps this to HTTP 503 so operators see "feature disabled" rather
// than an opaque 500.
var ErrPasskeyNotConfigured = errors.New("passkey support not configured")

// errPasskeyNameControlChars rejects names that carry ASCII control bytes —
// they are never legitimate user input here and only show up via copy/paste
// from terminal escape sequences. audit 2026-05-12 F-18.
var errPasskeyNameControlChars = errors.New("name contains control characters")

// passkeyNameHasControlChars returns true if name contains any rune in the
// C0 control range (< 0x20) or DEL (0x7F). Used by both the rename path and
// the registration normalization step.
func passkeyNameHasControlChars(name string) bool {
	for _, r := range name {
		if r < 0x20 || r == 0x7F {
			return true
		}
	}
	return false
}

// truncate200 caps s to 200 runes for safe inclusion in an audit_log row.
// audit 2026-05-12 F-14: keeps the bounded-size invariant when we log raw
// library/error text whose length we don't control.
func truncate200(s string) string {
	if runes := []rune(s); len(runes) > 200 {
		return string(runes[:200])
	}
	return s
}

// errChallengeOwnerMismatch fires when a register-finish call presents a
// challenge_token that was issued to a different user. The most common
// cause is a tab swap mid-ceremony; we refuse rather than silently
// register the credential on the wrong account.
var errChallengeOwnerMismatch = errors.New("challenge does not belong to this user")

// passkeyLoginSessions stores in-flight discoverable-login SessionData
// keyed by a random challenge token. Discoverable login doesn't know the
// user at Begin time, but user_action_tokens.user_id is NOT NULL — so we
// keep the state in memory rather than churn the schema. Sessions are
// short-lived (5 min) and a janitor goroutine sweeps expired entries.
//
// Process-wide singleton so the Begin and Finish halves of a ceremony,
// served by any goroutine, agree on the same map. Restarts wipe in-flight
// login attempts; the client just retries.
type passkeyLoginSessions struct {
	mu       sync.Mutex
	sessions map[string]passkeyLoginEntry
}

type passkeyLoginEntry struct {
	data      libwa.SessionData
	expiresAt time.Time
}

var passkeyLoginStore = &passkeyLoginSessions{
	sessions: make(map[string]passkeyLoginEntry),
}

// passkeyLoginJanitor scans the in-memory map and drops expired entries.
// Started lazily on the first Begin call so test packages that never
// trigger a login don't spawn a goroutine.
var passkeyLoginJanitorOnce sync.Once

func startPasskeyLoginJanitor() {
	passkeyLoginJanitorOnce.Do(func() {
		go func() {
			t := time.NewTicker(2 * time.Minute)
			defer t.Stop()
			for range t.C {
				passkeyLoginStore.gc()
			}
		}()
	})
}

func (p *passkeyLoginSessions) put(token string, sd libwa.SessionData, ttl time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sessions[token] = passkeyLoginEntry{data: sd, expiresAt: time.Now().Add(ttl)}
}

// take pulls (and removes) an entry, returning ok=false if missing or
// expired. The remove-on-take semantics make every challenge single-use.
func (p *passkeyLoginSessions) take(token string) (libwa.SessionData, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	e, ok := p.sessions[token]
	if !ok {
		return libwa.SessionData{}, false
	}
	delete(p.sessions, token)
	if time.Now().After(e.expiresAt) {
		return libwa.SessionData{}, false
	}
	return e.data, true
}

func (p *passkeyLoginSessions) gc() {
	now := time.Now()
	p.mu.Lock()
	defer p.mu.Unlock()
	for k, v := range p.sessions {
		if now.After(v.expiresAt) {
			delete(p.sessions, k)
		}
	}
}

// EnsureWebAuthnHandle returns the user's persistent 16-byte handle,
// generating + persisting it on first call. The handle is what we send
// to the authenticator as the "user id" — it MUST NOT contain PII per
// W3C §14.6, so we don't use email/UUID directly.
func (s *Store) EnsureWebAuthnHandle(ctx context.Context, userID uuid.UUID) ([]byte, error) {
	var handle []byte
	err := s.Pool.QueryRow(ctx,
		`SELECT webauthn_handle FROM users WHERE id = $1`, userID,
	).Scan(&handle)
	if err != nil {
		return nil, err
	}
	if len(handle) > 0 {
		return handle, nil
	}
	handle = make([]byte, 16)
	if _, err := rand.Read(handle); err != nil {
		return nil, err
	}
	_, err = s.Pool.Exec(ctx,
		`UPDATE users SET webauthn_handle = $2 WHERE id = $1 AND webauthn_handle IS NULL`,
		userID, handle)
	if err != nil {
		// Unique-collision retry: someone else won the race; reload.
		if pgIsUniqueViolation(err) {
			var hot []byte
			qerr := s.Pool.QueryRow(ctx,
				`SELECT webauthn_handle FROM users WHERE id = $1`, userID,
			).Scan(&hot)
			return hot, qerr
		}
		return nil, err
	}
	// If the UPDATE matched zero rows another writer beat us to the
	// handle; reload the row we already validated exists.
	if len(handle) == 16 {
		var hot []byte
		if err := s.Pool.QueryRow(ctx,
			`SELECT webauthn_handle FROM users WHERE id = $1`, userID,
		).Scan(&hot); err == nil && len(hot) > 0 {
			return hot, nil
		}
	}
	return handle, nil
}

// loadUserCredentials reads the user's registered authenticators so they
// can be supplied to BeginRegistration (for `excludeCredentials`, W3C
// §7.1 step 16) or to FinishRegistration / login.
func (s *Store) loadUserCredentials(ctx context.Context, userID uuid.UUID) ([]libwa.Credential, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT credential_id, public_key, sign_count, transports,
		       backup_eligible, backup_state,
		       COALESCE(aaguid, '00000000-0000-0000-0000-000000000000'::uuid)
		FROM user_credentials
		WHERE user_id = $1`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var creds []libwa.Credential
	for rows.Next() {
		var (
			credID, pubKey []byte
			signCount      int64
			transports     []string
			backupEligible bool
			backupState    bool
			aaguid         uuid.UUID
		)
		if err := rows.Scan(&credID, &pubKey, &signCount, &transports,
			&backupEligible, &backupState, &aaguid); err != nil {
			return nil, err
		}
		var aaguidBytes []byte
		if aaguid != uuid.Nil {
			aaguidBytes = aaguid[:]
		}
		creds = append(creds, webauthn.ConvertCredential(
			credID, pubKey, uint32(signCount), transports,
			backupEligible, backupState, aaguidBytes,
		))
	}
	return creds, rows.Err()
}

// optionsToMap round-trips a typed library struct through JSON to land
// in the generic map[string]any shape that apitypes.WebAuthn*Response
// uses. Saves the API layer from depending on protocol package types.
func optionsToMap(v any) (map[string]any, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	out := map[string]any{}
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// BeginPasskeyRegistration starts a credential-create ceremony for u.
// The returned ChallengeToken is opaque — the client echoes it back on
// finish so we can re-load the matching SessionData.
func (s *Store) BeginPasskeyRegistration(ctx context.Context, u User) (apitypes.WebAuthnRegisterBeginResponse, error) {
	if s.Webauthn == nil {
		return apitypes.WebAuthnRegisterBeginResponse{}, ErrPasskeyNotConfigured
	}

	handle, err := s.EnsureWebAuthnHandle(ctx, u.ID)
	if err != nil {
		return apitypes.WebAuthnRegisterBeginResponse{}, fmt.Errorf("webauthn handle: %w", err)
	}

	creds, err := s.loadUserCredentials(ctx, u.ID)
	if err != nil {
		return apitypes.WebAuthnRegisterBeginResponse{}, fmt.Errorf("load credentials: %w", err)
	}

	waUser := &webauthn.User{
		Handle:      handle,
		Name:        u.Email,
		DisplayName: u.Email,
		Creds:       creds,
	}

	creation, sessionData, err := s.Webauthn.WA.BeginRegistration(waUser)
	if err != nil {
		return apitypes.WebAuthnRegisterBeginResponse{}, fmt.Errorf("webauthn begin registration: %w", err)
	}

	sdJSON, err := json.Marshal(sessionData)
	if err != nil {
		return apitypes.WebAuthnRegisterBeginResponse{}, fmt.Errorf("marshal session data: %w", err)
	}

	// Stash via the existing action-token machinery. We pass the JSON as
	// a string so the JSONB round-trip in CreateActionToken doesn't
	// destructure the binary fields inside SessionData.
	token, err := s.CreateActionToken(ctx, u.ID, "webauthn_register", 5*time.Minute,
		map[string]any{"session_data": string(sdJSON)}, "")
	if err != nil {
		return apitypes.WebAuthnRegisterBeginResponse{}, fmt.Errorf("create challenge token: %w", err)
	}

	opts, err := optionsToMap(creation)
	if err != nil {
		return apitypes.WebAuthnRegisterBeginResponse{}, fmt.Errorf("encode options: %w", err)
	}

	return apitypes.WebAuthnRegisterBeginResponse{
		ChallengeToken: token,
		Options:        opts,
	}, nil
}

// FinishPasskeyRegistration verifies the browser's PublicKeyCredential
// against the stored SessionData and persists the new credential.
func (s *Store) FinishPasskeyRegistration(ctx context.Context, userID uuid.UUID, challengeToken string, credentialJSON []byte, name string) (apitypes.Passkey, error) {
	if s.Webauthn == nil {
		return apitypes.Passkey{}, ErrPasskeyNotConfigured
	}

	tokenUser, payload, err := s.ConsumeActionToken(ctx, challengeToken, "webauthn_register")
	if err != nil {
		return apitypes.Passkey{}, err
	}
	if tokenUser != userID {
		return apitypes.Passkey{}, errChallengeOwnerMismatch
	}

	sdStr, _ := payload["session_data"].(string)
	if sdStr == "" {
		return apitypes.Passkey{}, errors.New("challenge payload missing session_data")
	}
	var sessionData libwa.SessionData
	if err := json.Unmarshal([]byte(sdStr), &sessionData); err != nil {
		return apitypes.Passkey{}, fmt.Errorf("decode session data: %w", err)
	}

	// Re-load the user envelope; the WebAuthn library uses Handle as
	// the user.id check, so we must hand it back exactly.
	var (
		u      User
		handle []byte
	)
	if err := s.Pool.QueryRow(ctx, `
		SELECT id, email, role, created_at, webauthn_handle
		FROM users WHERE id = $1`, userID,
	).Scan(&u.ID, &u.Email, &u.Role, &u.CreatedAt, &handle); err != nil {
		return apitypes.Passkey{}, fmt.Errorf("reload user: %w", err)
	}
	if len(handle) == 0 {
		return apitypes.Passkey{}, errors.New("user has no webauthn handle")
	}
	creds, err := s.loadUserCredentials(ctx, userID)
	if err != nil {
		return apitypes.Passkey{}, fmt.Errorf("load credentials: %w", err)
	}
	waUser := &webauthn.User{
		Handle:      handle,
		Name:        u.Email,
		DisplayName: u.Email,
		Creds:       creds,
	}

	parsed, err := protocol.ParseCredentialCreationResponseBody(bytes.NewReader(credentialJSON))
	if err != nil {
		return apitypes.Passkey{}, fmt.Errorf("parse credential: %w", err)
	}
	cred, err := s.Webauthn.WA.CreateCredential(waUser, sessionData, parsed)
	if err != nil {
		return apitypes.Passkey{}, fmt.Errorf("verify credential: %w", err)
	}

	// AAGUID may be empty (e.g. attestation=none with privacy-respecting
	// authenticators). Only persist when it's the 16-byte form a UUID
	// can hold.
	var aaguidArg any
	var aaguidStr string
	if len(cred.Authenticator.AAGUID) == 16 {
		var au uuid.UUID
		copy(au[:], cred.Authenticator.AAGUID)
		if au != uuid.Nil {
			aaguidArg = au
			aaguidStr = au.String()
		}
	}

	transports := make([]string, 0, len(cred.Transport))
	for _, t := range cred.Transport {
		transports = append(transports, string(t))
	}

	// Normalize name: trim, default, cap at 64 runes.
	name = strings.TrimSpace(name)
	if name == "" {
		name = "Passkey"
	}
	// audit 2026-05-12 F-18: reject control characters in supplied names.
	if passkeyNameHasControlChars(name) {
		return apitypes.Passkey{}, errPasskeyNameControlChars
	}
	if runes := []rune(name); len(runes) > 64 {
		name = string(runes[:64])
	}

	attestationType := cred.AttestationType
	if attestationType == "" {
		attestationType = "none"
	}

	var (
		credUUID  uuid.UUID
		createdAt time.Time
	)
	err = s.Pool.QueryRow(ctx, `
		INSERT INTO user_credentials
			(user_id, credential_id, public_key, aaguid, sign_count, transports,
			 backup_eligible, backup_state, attestation_type, name)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		RETURNING id, created_at`,
		userID, cred.ID, cred.PublicKey, aaguidArg,
		int64(cred.Authenticator.SignCount), transports,
		cred.Flags.BackupEligible, cred.Flags.BackupState,
		attestationType, name,
	).Scan(&credUUID, &createdAt)
	if err != nil {
		return apitypes.Passkey{}, fmt.Errorf("insert user_credentials: %w", err)
	}

	// Audit. Best-effort.
	detail := map[string]any{
		"name":         name,
		"aaguid":       aaguidStr,
		"backup_state": cred.Flags.BackupState,
	}
	if db, jerr := json.Marshal(detail); jerr == nil {
		if aerr := s.AuditLog(ctx, "user:"+u.Email, "user.passkey.register", credUUID.String(), string(db)); aerr != nil {
			slog.Warn("audit_log insert (user.passkey.register)", "err", aerr)
		}
	}

	return apitypes.Passkey{
		ID:             credUUID.String(),
		Name:           name,
		AAGUID:         aaguidStr,
		Transports:     transports,
		BackupEligible: cred.Flags.BackupEligible,
		BackupState:    cred.Flags.BackupState,
		CreatedAt:      createdAt,
	}, nil
}

// BeginPasskeyLogin starts a discoverable-credential login. We don't
// know the user yet — the authenticator will send their userHandle in
// the assertion response, and our DiscoverableUserHandler maps that to
// the User row inside FinishPasskeyLogin.
func (s *Store) BeginPasskeyLogin(ctx context.Context) (apitypes.WebAuthnLoginBeginResponse, error) {
	if s.Webauthn == nil {
		return apitypes.WebAuthnLoginBeginResponse{}, ErrPasskeyNotConfigured
	}
	startPasskeyLoginJanitor()

	assertion, sessionData, err := s.Webauthn.WA.BeginDiscoverableLogin()
	if err != nil {
		return apitypes.WebAuthnLoginBeginResponse{}, fmt.Errorf("webauthn begin discoverable login: %w", err)
	}

	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return apitypes.WebAuthnLoginBeginResponse{}, fmt.Errorf("rand: %w", err)
	}
	token := fmt.Sprintf("mon_wa_%x", tokenBytes)

	passkeyLoginStore.put(token, *sessionData, 5*time.Minute)

	opts, err := optionsToMap(assertion)
	if err != nil {
		return apitypes.WebAuthnLoginBeginResponse{}, fmt.Errorf("encode options: %w", err)
	}
	return apitypes.WebAuthnLoginBeginResponse{
		ChallengeToken: token,
		Options:        opts,
	}, nil
}

// FinishPasskeyLogin verifies the assertion, identifies the user from
// the authenticator's userHandle, updates the sign counter, and returns
// the User so the API layer can issue a session.
func (s *Store) FinishPasskeyLogin(ctx context.Context, challengeToken string, credentialJSON []byte) (User, error) {
	if s.Webauthn == nil {
		return User{}, ErrPasskeyNotConfigured
	}

	sessionData, ok := passkeyLoginStore.take(challengeToken)
	if !ok {
		return User{}, ErrActionTokenInvalid
	}

	parsed, err := protocol.ParseCredentialRequestResponseBody(bytes.NewReader(credentialJSON))
	if err != nil {
		return User{}, fmt.Errorf("parse credential: %w", err)
	}

	// Snapshot the user we resolve inside the handler so we can return
	// them after the library's validation step. The handler runs once
	// per call; we save the row here rather than re-querying after.
	var (
		resolvedUser   User
		resolvedHandle []byte
		disabled       bool
	)

	handler := func(rawID, userHandle []byte) (libwa.User, error) {
		var (
			u           User
			handle      []byte
			disabledAt  *time.Time
			totpEnabled *time.Time
		)
		err := s.Pool.QueryRow(ctx, `
			SELECT u.id, u.email, u.role, u.created_at, u.disabled_at,
			       u.webauthn_handle, t.enabled_at
			FROM users u
			LEFT JOIN user_totp t ON t.user_id = u.id
			WHERE u.webauthn_handle = $1`, userHandle,
		).Scan(&u.ID, &u.Email, &u.Role, &u.CreatedAt, &disabledAt, &handle, &totpEnabled)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrUserNotFound
		}
		if err != nil {
			return nil, err
		}
		if disabledAt != nil {
			u.Disabled = true
			disabled = true
			// Still build a valid libwa.User — letting the library
			// finish lets us produce a stable error; we recheck after.
		}
		u.TOTPActive = totpEnabled != nil

		creds, err := s.loadUserCredentials(ctx, u.ID)
		if err != nil {
			return nil, err
		}

		resolvedUser = u
		resolvedHandle = handle

		return &webauthn.User{
			Handle:      handle,
			Name:        u.Email,
			DisplayName: u.Email,
			Creds:       creds,
		}, nil
	}

	cred, err := s.Webauthn.WA.ValidateDiscoverableLogin(handler, sessionData, parsed)
	if err != nil {
		// audit 2026-05-12 F-14: record validation failures so an operator
		// can spot brute-force or credential-stuffing patterns. We do not
		// know the user reliably here (the assertion may not have resolved
		// one), so the actor is "anon".
		if aerr := s.AuditLog(ctx, "anon", "user.passkey.login.failed", "", truncate200(err.Error())); aerr != nil {
			slog.Warn("audit_log insert (user.passkey.login.failed)", "err", aerr)
		}
		return User{}, fmt.Errorf("verify assertion: %w", err)
	}
	if disabled {
		// audit 2026-05-12 F-14: disabled-user attempts are auditable; the
		// handler resolved a real user so attribute by email.
		actor := "anon"
		if resolvedUser.Email != "" {
			actor = "user:" + resolvedUser.Email
		}
		if aerr := s.AuditLog(ctx, actor, "user.passkey.login.failed", resolvedUser.ID.String(), truncate200("user disabled")); aerr != nil {
			slog.Warn("audit_log insert (user.passkey.login.failed)", "err", aerr)
		}
		return User{}, ErrUserDisabled
	}
	if resolvedUser.ID == uuid.Nil || len(resolvedHandle) == 0 {
		return User{}, ErrUserNotFound
	}

	// audit 2026-05-12 F-12: persist backup_state. The authenticator may
	// have toggled its sync/backup state since registration — if we never
	// store the new value, force-mode checks that care about
	// backup_eligible+backup_state will run against stale data.
	// audit 2026-05-12 F-13: refuse to accept a sign_count that did not
	// strictly increase. A stationary or regressing counter is the
	// classic FIDO clone/replay signal; the W3C spec says the RP MAY
	// refuse the assertion in that case (we do).
	credHex := fmt.Sprintf("%x", cred.ID)
	if cred.Authenticator.CloneWarning {
		_ = s.AuditLog(ctx, "user:"+resolvedUser.Email, "user.passkey.clone_warning",
			resolvedUser.ID.String(),
			"go-webauthn flagged CloneWarning on credential "+credHex)
		slog.Warn("webauthn: clone warning on credential",
			"user_id", resolvedUser.ID, "credential_id", credHex)
		return User{}, errors.New("passkey clone warning: sign counter did not advance")
	}
	tag, uerr := s.Pool.Exec(ctx, `
		UPDATE user_credentials
		   SET sign_count   = $2,
		       last_used_at = now(),
		       backup_state = $3
		 WHERE credential_id = $1 AND sign_count < $2`,
		cred.ID, int64(cred.Authenticator.SignCount), cred.Flags.BackupState,
	)
	if uerr != nil {
		slog.Warn("user_credentials sign_count update", "err", uerr)
	} else if tag.RowsAffected() == 0 {
		_ = s.AuditLog(ctx, "user:"+resolvedUser.Email, "user.passkey.clone_warning",
			resolvedUser.ID.String(),
			"sign_count did not advance for credential "+credHex)
		slog.Warn("webauthn: sign_count did not advance; refusing session",
			"user_id", resolvedUser.ID, "credential_id", credHex)
		return User{}, errors.New("passkey clone warning: sign counter did not advance")
	}

	if aerr := s.AuditLog(ctx, "user:"+resolvedUser.Email, "user.passkey.login.success", resolvedUser.ID.String(), ""); aerr != nil {
		slog.Warn("audit_log insert (user.passkey.login.success)", "err", aerr)
	}

	return resolvedUser, nil
}

// ListPasskeys returns the user's registered passkeys in registration
// order. AAGUID is omitted (empty) when the authenticator declined to
// supply one.
func (s *Store) ListPasskeys(ctx context.Context, userID uuid.UUID) ([]apitypes.Passkey, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT id, name,
		       COALESCE(aaguid, '00000000-0000-0000-0000-000000000000'::uuid),
		       transports, backup_eligible, backup_state, created_at, last_used_at
		FROM user_credentials
		WHERE user_id = $1
		ORDER BY created_at ASC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []apitypes.Passkey{}
	for rows.Next() {
		var (
			id             uuid.UUID
			name           string
			aaguid         uuid.UUID
			transports     []string
			backupEligible bool
			backupState    bool
			createdAt      time.Time
			lastUsedAt     *time.Time
		)
		if err := rows.Scan(&id, &name, &aaguid, &transports, &backupEligible,
			&backupState, &createdAt, &lastUsedAt); err != nil {
			return nil, err
		}
		var aaguidStr string
		if aaguid != uuid.Nil {
			aaguidStr = aaguid.String()
		}
		out = append(out, apitypes.Passkey{
			ID:             id.String(),
			Name:           name,
			AAGUID:         aaguidStr,
			Transports:     transports,
			BackupEligible: backupEligible,
			BackupState:    backupState,
			CreatedAt:      createdAt,
			LastUsedAt:     lastUsedAt,
		})
	}
	return out, rows.Err()
}

// RenamePasskey updates the human-friendly label on a credential.
// Returns ErrUserNotFound when no row matches (wrong user or wrong id).
func (s *Store) RenamePasskey(ctx context.Context, userID, credID uuid.UUID, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("name required")
	}
	// audit 2026-05-12 F-18: reject ASCII control characters in passkey names.
	if passkeyNameHasControlChars(name) {
		return errPasskeyNameControlChars
	}
	if runes := []rune(name); len(runes) > 64 {
		return errors.New("name too long (max 64 characters)")
	}
	tag, err := s.Pool.Exec(ctx,
		`UPDATE user_credentials SET name = $3 WHERE id = $1 AND user_id = $2`,
		credID, userID, name)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrUserNotFound
	}
	return nil
}

// DeletePasskey unconditionally removes the credential row. The API
// layer is responsible for any policy gating ("don't let users drop
// their last passkey when force_mode=passkey_required") — keeping that
// out of here lets the store emit clean ErrUserNotFound responses.
func (s *Store) DeletePasskey(ctx context.Context, userID, credID uuid.UUID) error {
	tag, err := s.Pool.Exec(ctx,
		`DELETE FROM user_credentials WHERE id = $1 AND user_id = $2`,
		credID, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrUserNotFound
	}
	return nil
}

// DeleteAllPasskeysForUser wipes every credential row owned by userID.
// Returns the number of rows deleted. Intended for CLI recovery: an admin
// who's locked themselves out via a misconfigured passkey can shell into the
// container and wipe their own credentials to fall back on password+TOTP.
func (s *Store) DeleteAllPasskeysForUser(ctx context.Context, userID uuid.UUID) (int, error) {
	tag, err := s.Pool.Exec(ctx,
		`DELETE FROM user_credentials WHERE user_id = $1`, userID)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}
