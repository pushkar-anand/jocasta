package web

import (
	"errors"
	"fmt"
	"image/png"
	"net/http"
	"strings"

	"github.com/pushkar-anand/build-with-go/http/response"
	"github.com/pushkar-anand/jocasta/internal/auth"
)

// securityData is the settings page for the signed-in account's own 2FA
// state.
type securityData struct {
	view

	TOTPEnabled bool
	// Enrolling is true once a secret has been generated but not yet
	// confirmed. The page then shows the QR/manual key and a confirm form
	// in place of the enable button.
	Enrolling bool
	Secret    string

	RecoveryCodesRemaining int64

	// RecoveryCodes is the one-shot flash a confirm or regenerate leaves for
	// the GET it redirects to; see flashRecoveryCodes.
	RecoveryCodes []string

	// Error and ErrorAction keep a failed confirmation beside its field.
	Error       string
	ErrorAction string
}

// flashRecoveryCodes is where ConfirmTOTPEnrollment's and
// RegenerateRecoveryCodes' plaintext codes ride the redirect to the GET that
// shows them once.
const flashRecoveryCodes = "flash.recovery_codes"

// security serves the account's two-factor settings. Recovery codes from a
// preceding confirmation or regeneration are displayed only once.
func (h *Handler) security(sm *auth.Session, a *auth.Auth) response.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		return h.renderSecurity(w, r, sm, a, "", "")
	}
}

// renderSecurity shows security settings or a failed confirmation. Recovery
// codes are consumed from the one-shot flash only for a successful page load.
func (h *Handler) renderSecurity(w http.ResponseWriter, r *http.Request, sm *auth.Session, a *auth.Auth, action, message string) error {
	ctx := r.Context()

	userID, err := currentUserID(sm, r)
	if err != nil {
		return err
	}

	enabled, enrolling, secret, err := a.TOTPStatus(ctx, userID)
	if err != nil {
		return err
	}

	var remaining int64
	if enabled {
		remaining, err = a.RemainingRecoveryCodes(ctx, userID)
		if err != nil {
			return err
		}
	}

	var codes []string

	if message == "" {
		if flash := sm.PopFlash(ctx, flashRecoveryCodes); flash != "" {
			codes = strings.Split(flash, "\n")
		}
	}

	data := securityData{
		view: view{
			Title:      "Security",
			Section:    "Security",
			Role:       sm.CurrentRole(ctx),
			SignedInAs: sm.CurrentUsername(ctx),
		},
		TOTPEnabled:            enabled,
		Enrolling:              enrolling,
		Secret:                 secret,
		RecoveryCodesRemaining: remaining,
		RecoveryCodes:          codes,
		Error:                  message,
		ErrorAction:            action,
	}

	if message != "" {
		data.Title = "Error: Security"
		h.htmlWriter.Error(w, r, http.StatusUnprocessableEntity, templatePageSecurity, data)
	} else {
		h.htmlWriter.Success(w, r, templatePageSecurity, data)
	}

	return nil
}

// securityEnroll starts (or restarts) enrollment and sends the visitor back
// to the GET, which now finds a pending secret and shows the QR/confirm
// form.
func (h *Handler) securityEnroll(sm *auth.Session, a *auth.Auth) response.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		ctx := r.Context()

		userID, err := currentUserID(sm, r)
		if err != nil {
			return err
		}

		if _, err := a.StartTOTPEnrollment(ctx, userID, sm.CurrentUsername(ctx)); err != nil {
			return err
		}

		http.Redirect(w, r, "/settings/security", http.StatusSeeOther)

		return nil
	}
}

// securityConfirm enables two-factor authentication after checking the pending
// authenticator's code, then redirects to display the recovery codes once.
func (h *Handler) securityConfirm(sm *auth.Session, a *auth.Auth) response.HandlerFunc {
	type confirmForm struct {
		Code string `schema:"code" validate:"required,min=6,max=6"`
	}

	return func(w http.ResponseWriter, r *http.Request) error {
		ctx := r.Context()

		userID, err := currentUserID(sm, r)
		if err != nil {
			return err
		}

		input, err := h.reader.ReadAndValidateForm[confirmForm](r)
		if err != nil {
			if p, ok := errors.AsType[response.Problem](err); ok && p.Status() == http.StatusUnprocessableEntity {
				return h.renderSecurity(w, r, sm, a, "confirm", "Enter the 6-digit code your authenticator app shows now.")
			}

			return err
		}

		codes, err := a.ConfirmTOTPEnrollment(ctx, userID, input.Code)
		if err != nil {
			if errors.Is(err, auth.ErrInvalidEnrollmentCode) {
				return h.renderSecurity(w, r, sm, a, "confirm", "That code did not work. Enter the 6-digit code your authenticator app shows now.")
			}

			return err
		}

		sm.Flash(ctx, flashRecoveryCodes, strings.Join(codes, "\n"))
		http.Redirect(w, r, "/settings/security", http.StatusSeeOther)

		return nil
	}
}

// securityDisable turns off two-factor authentication after checking the
// account's password, then redirects to its security settings.
func (h *Handler) securityDisable(sm *auth.Session, a *auth.Auth) response.HandlerFunc {
	type disableForm struct {
		// Username is the hidden field that tells a password manager which
		// login to fill. It is ignored: the session says whose account this is.
		Username string `schema:"username"`
		Password string `schema:"password" validate:"required,min=8,max=1000"`
	}

	return func(w http.ResponseWriter, r *http.Request) error {
		ctx := r.Context()

		userID, err := currentUserID(sm, r)
		if err != nil {
			return err
		}

		input, err := h.reader.ReadAndValidateForm[disableForm](r)
		if err != nil {
			if p, ok := errors.AsType[response.Problem](err); ok && p.Status() == http.StatusUnprocessableEntity {
				return h.renderSecurity(w, r, sm, a, "disable", "Enter your password, between 8 and 1000 characters.")
			}

			return err
		}

		if err := a.DisableTOTP(ctx, userID, input.Password); err != nil {
			if errors.Is(err, auth.ErrInvalidPassword) {
				return h.renderSecurity(w, r, sm, a, "disable", "That password did not match. Enter your account password and try again.")
			}

			return err
		}

		http.Redirect(w, r, "/settings/security", http.StatusSeeOther)

		return nil
	}
}

// securityRegenerateRecoveryCodes checks the password and replaces the recovery
// codes, then redirects to display their plaintext values once.
func (h *Handler) securityRegenerateRecoveryCodes(sm *auth.Session, a *auth.Auth) response.HandlerFunc {
	type regenerateForm struct {
		// Username is ignored, as for disabling two-factor.
		Username string `schema:"username"`
		Password string `schema:"password" validate:"required,min=8,max=1000"`
	}

	return func(w http.ResponseWriter, r *http.Request) error {
		ctx := r.Context()

		userID, err := currentUserID(sm, r)
		if err != nil {
			return err
		}

		input, err := h.reader.ReadAndValidateForm[regenerateForm](r)
		if err != nil {
			if p, ok := errors.AsType[response.Problem](err); ok && p.Status() == http.StatusUnprocessableEntity {
				return h.renderSecurity(w, r, sm, a, "regenerate", "Enter your password, between 8 and 1000 characters.")
			}

			return err
		}

		codes, err := a.RegenerateRecoveryCodes(ctx, userID, input.Password)
		if err != nil {
			if errors.Is(err, auth.ErrInvalidPassword) {
				return h.renderSecurity(w, r, sm, a, "regenerate", "That password did not match. Enter your account password and try again.")
			}

			return err
		}

		sm.Flash(ctx, flashRecoveryCodes, strings.Join(codes, "\n"))
		http.Redirect(w, r, "/settings/security", http.StatusSeeOther)

		return nil
	}
}

// totpQR serves the pending enrollment's QR code as a same-origin image, so
// the CSP's default-src 'self' (no img-src override) never has to admit a
// data: URI. It writes the image bytes straight to the response, with no
// template.
func (h *Handler) totpQR(sm *auth.Session, a *auth.Auth) response.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		userID, err := currentUserID(sm, r)
		if err != nil {
			return err
		}

		key, err := a.PendingTOTPKey(r.Context(), userID)
		if err != nil {
			return err
		}

		img, err := key.Image(256, 256)
		if err != nil {
			return fmt.Errorf("render totp qr: %w", err)
		}

		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "no-store")

		return png.Encode(w, img)
	}
}
