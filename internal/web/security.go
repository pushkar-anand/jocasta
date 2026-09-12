package web

import (
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
	// confirmed -- the page shows the QR/manual key and a confirm form
	// instead of the enable button.
	Enrolling bool
	Secret    string

	RecoveryCodesRemaining int64

	// RecoveryCodes is the one-shot flash a confirm or regenerate leaves for
	// the GET it redirects to -- see flashRecoveryCodes.
	RecoveryCodes []string
}

// flashRecoveryCodes is where ConfirmTOTPEnrollment's and
// RegenerateRecoveryCodes' plaintext codes ride the redirect to the GET that
// shows them once.
const flashRecoveryCodes = "flash.recovery_codes"

func (h *Handler) security(sm *auth.Session, a *auth.Auth) response.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
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
		if flash := sm.PopFlash(ctx, flashRecoveryCodes); flash != "" {
			codes = strings.Split(flash, "\n")
		}

		h.htmlWriter.Success(w, r, templatePageSecurity, securityData{
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
		})

		return nil
	}
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
			return err
		}

		codes, err := a.ConfirmTOTPEnrollment(ctx, userID, input.Code)
		if err != nil {
			return err
		}

		sm.Flash(ctx, flashRecoveryCodes, strings.Join(codes, "\n"))
		http.Redirect(w, r, "/settings/security", http.StatusSeeOther)

		return nil
	}
}

func (h *Handler) securityDisable(sm *auth.Session, a *auth.Auth) response.HandlerFunc {
	type disableForm struct {
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
			return err
		}

		if err := a.DisableTOTP(ctx, userID, input.Password); err != nil {
			return err
		}

		http.Redirect(w, r, "/settings/security", http.StatusSeeOther)

		return nil
	}
}

func (h *Handler) securityRegenerateRecoveryCodes(sm *auth.Session, a *auth.Auth) response.HandlerFunc {
	type regenerateForm struct {
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
			return err
		}

		codes, err := a.RegenerateRecoveryCodes(ctx, userID, input.Password)
		if err != nil {
			return err
		}

		sm.Flash(ctx, flashRecoveryCodes, strings.Join(codes, "\n"))
		http.Redirect(w, r, "/settings/security", http.StatusSeeOther)

		return nil
	}
}

// totpQR serves the pending enrollment's QR code as a same-origin image, so
// the CSP's default-src 'self' (no img-src override) never has to admit a
// data: URI. It never renders through htmlWriter -- there's no template
// involved, just image bytes written directly to the response.
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
