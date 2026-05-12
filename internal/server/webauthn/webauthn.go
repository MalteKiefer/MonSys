// Package webauthn wraps github.com/go-webauthn/webauthn for the MonSys API.
// Centralizes RP (Relying Party) config so handlers don't each construct it,
// and provides a thin User adapter so the store layer can keep its native
// store.User shape without depending on go-webauthn internals.
package webauthn

import (
	"errors"
	"fmt"

	"github.com/go-webauthn/webauthn/protocol"
	libwa "github.com/go-webauthn/webauthn/webauthn"
)

// Service is the configured WebAuthn relying party. One instance per process.
type Service struct {
	WA *libwa.WebAuthn
}

// Config holds the RP identity. Provided by main from env vars.
//
//	RPID:     RP identifier — bare hostname (e.g. "mon.kiefer-networks.de").
//	          MUST NOT contain scheme or port. The browser checks that the
//	          current document's origin's hostname == RPID OR is a registrable
//	          suffix of it.
//	RPName:   Human-readable name shown in the browser UI.
//	Origins:  List of allowed full origins (scheme://host[:port]). The browser
//	          includes its origin in clientDataJSON; we accept the credential
//	          only if it matches one of these.
type Config struct {
	RPID    string
	RPName  string
	Origins []string
}

// New configures a Service. Validates RPID/origin shape so misconfiguration
// is caught at startup rather than on the first registration.
func New(cfg Config) (*Service, error) {
	if cfg.RPID == "" {
		return nil, errors.New("webauthn: RPID required")
	}
	if cfg.RPName == "" {
		cfg.RPName = "MonSys"
	}
	if len(cfg.Origins) == 0 {
		return nil, errors.New("webauthn: at least one origin required")
	}

	wa, err := libwa.New(&libwa.Config{
		RPID:          cfg.RPID,
		RPDisplayName: cfg.RPName,
		RPOrigins:     cfg.Origins,
		AuthenticatorSelection: protocol.AuthenticatorSelection{
			ResidentKey:      protocol.ResidentKeyRequirementRequired,
			UserVerification: protocol.VerificationRequired,
		},
		AttestationPreference: protocol.PreferNoAttestation,
	})
	if err != nil {
		return nil, fmt.Errorf("webauthn: %w", err)
	}
	return &Service{WA: wa}, nil
}

// User adapts a MonSys user (and their already-loaded credentials) to the
// go-webauthn User interface. Pass an empty Credentials slice if the user
// hasn't enrolled any yet (registration path).
type User struct {
	Handle      []byte // webauthn_handle column — 16 random bytes per user
	Name        string // login name (e.g. email)
	DisplayName string // human-readable name
	Creds       []libwa.Credential
}

func (u *User) WebAuthnID() []byte                      { return u.Handle }
func (u *User) WebAuthnName() string                    { return u.Name }
func (u *User) WebAuthnDisplayName() string             { return u.DisplayName }
func (u *User) WebAuthnCredentials() []libwa.Credential { return u.Creds }

// ConvertCredential is a convenience for callers that have raw bytes from
// the DB (credential_id, public_key, sign_count, transports, backup flags,
// aaguid) and want a libwa.Credential they can stuff into User.Creds.
func ConvertCredential(credID, pubKey []byte, signCount uint32, transports []string, backupEligible, backupState bool, aaguid []byte) libwa.Credential {
	transportsTyped := make([]protocol.AuthenticatorTransport, 0, len(transports))
	for _, t := range transports {
		transportsTyped = append(transportsTyped, protocol.AuthenticatorTransport(t))
	}
	return libwa.Credential{
		ID:              credID,
		PublicKey:       pubKey,
		AttestationType: "none",
		Transport:       transportsTyped,
		Flags: libwa.CredentialFlags{
			UserPresent:    true,
			UserVerified:   true,
			BackupEligible: backupEligible,
			BackupState:    backupState,
		},
		Authenticator: libwa.Authenticator{
			AAGUID:    aaguid,
			SignCount: signCount,
		},
	}
}
