package web

import (
	"errors"
	"fmt"
	"image/png"
	"net/http"

	"github.com/pushkar-anand/build-with-go/http/response"
	"github.com/pushkar-anand/jocasta/internal/auth"
)

// securityData is the settings page for the 2FA state of the signed-in
// account.
type securityData struct {
	view

	TOTPEnabled bool
	// Enrolling is true when a secret exists but is not confirmed yet. The
	// page then shows the QR/manual key and a confirm form, instead of the
	// enable button.
	Enrolling bool
	Secret    string

	RecoveryCodesRemaining int64

	// RecoveryCodes are the plaintext codes from a confirm or regenerate. They
	// are set only on the page that answers that POST. They are never stored.
	// So only that page shows them.
	RecoveryCodes []string

	// Error and ErrorAction keep a failed confirmation next to its field.
	Error       string
	ErrorAction string
}

// security serves the two-factor settings of the account.
func (h *Handler) security(sm *auth.Session, a *auth.Auth) response.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		return h.renderSecurity(w, r, sm, a, nil, "", "")
	}
}

// renderSecurity shows security settings. It shows the recovery codes from a
// confirm or regenerate. Or it shows the message of a failed confirmation next
// to the form that action names.
func (h *Handler) renderSecurity(w http.ResponseWriter, r *http.Request, sm *auth.Session, a *auth.Auth, codes []string, action, message string) error {
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

// securityEnroll starts (or starts again) enrollment, and sends the visitor
// back to the GET. The GET now finds a pending secret, and shows the
// QR/confirm form. If 2FA is already on, as from an old tab, it changes
// nothing. It then sends the visitor to the same page, which says that 2FA is
// on.
func (h *Handler) securityEnroll(sm *auth.Session, a *auth.Auth) response.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		ctx := r.Context()

		userID, err := currentUserID(sm, r)
		if err != nil {
			return err
		}

		if _, err := a.StartTOTPEnrollment(ctx, userID, sm.CurrentUsername(ctx)); err != nil && !errors.Is(err, auth.ErrTOTPEnabled) {
			return err
		}

		http.Redirect(w, r, "/settings/security", http.StatusSeeOther)

		return nil
	}
}

// securityConfirm turns on two-factor authentication after it checks the code
// of the pending authenticator. It then answers with the page of the recovery
// codes. The page is the response of the POST, not a redirect. So the codes
// are never stored, also not in the session. If 2FA is already on, it
// redirects and makes no codes.
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
				return h.renderSecurity(w, r, sm, a, nil, "confirm", "Enter the 6-digit code your authenticator app shows now.")
			}

			return err
		}

		codes, err := a.ConfirmTOTPEnrollment(ctx, userID, input.Code)

		switch {
		case errors.Is(err, auth.ErrTOTPEnabled):
			http.Redirect(w, r, "/settings/security", http.StatusSeeOther)
			return nil
		case errors.Is(err, auth.ErrInvalidEnrollmentCode):
			return h.renderSecurity(w, r, sm, a, nil, "confirm", "That code did not work. Enter the 6-digit code your authenticator app shows now.")
		case err != nil:
			return err
		}

		return h.renderSecurity(w, r, sm, a, codes, "", "")
	}
}

// securityDisable turns off two-factor authentication after it checks the
// password of the account. It then redirects to its security settings.
func (h *Handler) securityDisable(sm *auth.Session, a *auth.Auth) response.HandlerFunc {
	type disableForm struct {
		// Username is the hidden field that tells a password manager which
		// login to fill. The handler ignores it. The session tells whose
		// account this is.
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
				return h.renderSecurity(w, r, sm, a, nil, "disable", "Enter your password, between 8 and 1000 characters.")
			}

			return err
		}

		if err := a.DisableTOTP(ctx, userID, input.Password); err != nil {
			if errors.Is(err, auth.ErrInvalidPassword) {
				return h.renderSecurity(w, r, sm, a, nil, "disable", "That password did not match. Enter your account password and try again.")
			}

			return err
		}

		http.Redirect(w, r, "/settings/security", http.StatusSeeOther)

		return nil
	}
}

// securityRegenerateRecoveryCodes checks the password and replaces the recovery
// codes. It then answers with the page of the codes, as securityConfirm
// does.
func (h *Handler) securityRegenerateRecoveryCodes(sm *auth.Session, a *auth.Auth) response.HandlerFunc {
	type regenerateForm struct {
		// The handler ignores Username, as when it turns off two-factor.
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
				return h.renderSecurity(w, r, sm, a, nil, "regenerate", "Enter your password, between 8 and 1000 characters.")
			}

			return err
		}

		codes, err := a.RegenerateRecoveryCodes(ctx, userID, input.Password)
		if err != nil {
			if errors.Is(err, auth.ErrInvalidPassword) {
				return h.renderSecurity(w, r, sm, a, nil, "regenerate", "That password did not match. Enter your account password and try again.")
			}

			return err
		}

		return h.renderSecurity(w, r, sm, a, codes, "", "")
	}
}

// totpQR serves the QR code of the pending enrollment as a same-origin image.
// So the default-src 'self' of the CSP (no img-src override) never needs to
// allow a data: URI. It writes the image bytes directly to the response, with
// no template.
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
