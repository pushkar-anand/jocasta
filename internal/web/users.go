package web

import (
	"context"
	"errors"
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
	Role      string
	CreatedAt time.Time
}

func newUserRow(u *models.User) userRow {
	return userRow{
		ID:        u.ID,
		Username:  u.Username,
		Role:      string(u.Role),
		CreatedAt: u.CreatedAt.Time,
	}
}

// usersData is the settings page listing every account.
type usersData struct {
	view
	Users []userRow

	// Error is set only when createUser's own insert hit auth.ErrUsernameTaken
	// -- the list this page needs on redisplay is dynamic, which is what the
	// generic error pipeline (ErrorPageData) cannot supply, so this handler
	// re-renders itself instead of returning the error through it.
	Error string
}

// requireAdmin extends currentUserID's check with the one fact an admin-gated
// route additionally needs: that the signed-in account is an admin.
func requireAdmin(ctx context.Context, sm *auth.Session, a *auth.Auth, r *http.Request) error {
	userID, err := currentUserID(sm, r)
	if err != nil {
		return err
	}

	ok, err := a.IsAdmin(ctx, userID)
	if err != nil {
		return err
	}

	if !ok {
		return auth.ErrForbidden
	}

	return nil
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

// users serves the user management page.
func (h *Handler) users(sm *auth.Session, a *auth.Auth) response.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		ctx := r.Context()

		if err := requireAdmin(ctx, sm, a, r); err != nil {
			return err
		}

		list, err := userList(ctx, a)
		if err != nil {
			return err
		}

		h.htmlWriter.Success(w, r, templatePageUsers, usersData{
			Title: "Users",
			Users: list,
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

		if err := requireAdmin(ctx, sm, a, r); err != nil {
			return err
		}

		input, err := h.reader.ReadAndValidateForm[createUserForm](r)
		if err != nil {
			return err
		}

		_, createErr := a.CreateUser(ctx, input.Username, input.Password, dbtype.UserRole(input.Role))
		if createErr != nil && !errors.Is(createErr, auth.ErrUsernameTaken) {
			return createErr
		}

		list, err := userList(ctx, a)
		if err != nil {
			return err
		}

		data := usersData{
			Title: "Users",
			Users: list,
		}

		if createErr != nil {
			data.Error = "That username is already taken."
		}

		h.htmlWriter.Success(w, r, templatePageUsers, data)

		return nil
	}
}
