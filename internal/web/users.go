package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/pushkar-anand/build-with-go/http/response"
	"github.com/pushkar-anand/jocasta/internal/auth"
	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/internal/db/models"
)

// userRow is what the users page shows for one account.
type userRow struct {
	ID        int64
	Username  string
	Role      dbtype.UserRole
	CreatedAt time.Time
}

func newUserRow(u *models.User) userRow {
	return userRow{
		ID:        u.ID,
		Username:  u.Username,
		Role:      u.Role,
		CreatedAt: u.CreatedAt.Time,
	}
}

// usersData is the settings page listing every account.
type usersData struct {
	view
	Users []userRow

	// CurrentUserID marks the signed-in admin's own row.
	CurrentUserID int64

	// Created and Error are one-shot flashes from the createUser POST, read and
	// cleared by the GET it redirects to. Username and SelectedRole preserve the
	// non-secret form values alongside Error.
	Created      string
	Error        string
	Username     string
	SelectedRole string
}

// userList reads every account as the view the template renders.
func userList(ctx context.Context, a *auth.Auth) ([]userRow, error) {
	rows, err := a.ListUsers(ctx)
	if err != nil {
		return nil, err
	}

	list := make([]userRow, len(rows))
	for i, row := range rows {
		list[i] = newUserRow(row)
	}

	return list, nil
}

// One-shot flashes createUser leaves for the GET it redirects to. Only
// non-secret form values are carried back.
const (
	flashUserError    = "flash.user_error"
	flashUserCreated  = "flash.user_created"
	flashUserUsername = "flash.user_username"
	flashUserRole     = "flash.user_role"
)

// users serves the user management page. The route is gated to an admin, so
// the page always renders in the admin view.
func (h *Handler) users(sm *auth.Session, a *auth.Auth) response.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		ctx := r.Context()

		list, err := userList(ctx, a)
		if err != nil {
			return err
		}

		currentID, _ := sm.CurrentUserID(ctx)

		h.htmlWriter.Success(w, r, templatePageUsers, usersData{
			Title: "Users", Section: "Users", Role: dbtype.RoleAdmin,
			SignedInAs:    sm.CurrentUsername(ctx),
			Users:         list,
			CurrentUserID: currentID,
			Created:       sm.PopFlash(ctx, flashUserCreated),
			Error:         sm.PopFlash(ctx, flashUserError),
			Username:      sm.PopFlash(ctx, flashUserUsername),
			SelectedRole:  sm.PopFlash(ctx, flashUserRole),
		})

		return nil
	}
}

// createUser adds an account under the role the admin picked.
func (h *Handler) createUser(sm *auth.Session, a *auth.Auth) response.HandlerFunc {
	// oneof excludes admin: the account setup creates is the only admin this
	// instance ever has, so there's no second admin for this form to hand out.
	type createUserForm struct {
		Username string `schema:"username" validate:"required,min=3,max=100"`
		Password string `schema:"password" validate:"required,min=8,max=1000"`
		Role     string `schema:"role" validate:"required,oneof=read read_write"`
	}

	return func(w http.ResponseWriter, r *http.Request) error {
		ctx := r.Context()

		input, err := h.reader.ReadAndValidateForm[createUserForm](r)
		if err != nil {
			return err
		}

		_, createErr := a.CreateUser(ctx, input.Username, input.Password, dbtype.UserRole(input.Role))
		if createErr != nil && !errors.Is(createErr, auth.ErrUsernameTaken) {
			return createErr
		}

		switch {
		case errors.Is(createErr, auth.ErrUsernameTaken):
			sm.Flash(ctx, flashUserError, "That username is already taken.")
			sm.Flash(ctx, flashUserUsername, input.Username)
			sm.Flash(ctx, flashUserRole, input.Role)
		default:
			// No invite flow, so the confirmation points at sign-in.
			sm.Flash(ctx, flashUserCreated, fmt.Sprintf(
				"%s added as %s. They sign in at /login.",
				input.Username, roleDisplay(dbtype.UserRole(input.Role)),
			))
		}

		http.Redirect(w, r, "/settings/users", http.StatusSeeOther)

		return nil
	}
}
