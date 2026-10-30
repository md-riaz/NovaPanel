package verbs

import (
	"context"
	"database/sql"
	"errors"
	"net/mail"
	"novapanel/internal/agent"
	"novapanel/internal/auth"
	"novapanel/internal/id"
	"novapanel/internal/rpc"
	"novapanel/internal/store"
	"strings"
	"unicode"
)

type userCreateArgs struct {
	Username string `json:"username"`
	Email    string `json:"email"`
	Role     string `json:"role"`
	Password string `json:"password"`
}
type userUpdateArgs struct {
	UserID   string  `json:"user_id"`
	Email    *string `json:"email,omitempty"`
	Role     *string `json:"role,omitempty"`
	Disabled *bool   `json:"disabled,omitempty"`
}
type userIDArgs struct {
	UserID string `json:"user_id"`
}
type userPasswordArgs struct {
	UserID   string `json:"user_id"`
	Password string `json:"password"`
}

func init() {
	rpc.Register(rpc.Verb{Name: "user.create", MinRole: rpc.Owner, MFA: true, NewArgs: func() any { return &userCreateArgs{} }, Run: userCreate})
	rpc.Register(rpc.Verb{Name: "user.update", MinRole: rpc.Owner, MFA: true, NewArgs: func() any { return &userUpdateArgs{} }, Run: userUpdate})
	rpc.Register(rpc.Verb{Name: "user.delete", MinRole: rpc.Owner, MFA: true, NewArgs: func() any { return &userIDArgs{} }, Run: userDelete})
	rpc.Register(rpc.Verb{Name: "user.set_password", MinRole: rpc.Owner, MFA: true, NewArgs: func() any { return &userPasswordArgs{} }, Run: userSetPassword})
	rpc.Register(rpc.Verb{Name: "user.list", MinRole: rpc.Admin, ReadOnly: true, NewArgs: func() any { return &emptyArgs{} }, Run: userList})
}
func identityError(e error) error {
	if errors.Is(e, sql.ErrNoRows) {
		return rpc.Errf("not_found", "Identity not found")
	}
	if errors.Is(e, store.ErrAuthConflict) {
		return rpc.Errf("conflict", "Identity conflict")
	}
	if errors.Is(e, store.ErrAuthInvalid) || errors.Is(e, store.ErrAuthExpired) {
		return rpc.Errf("invalid_argument", "Invalid identity")
	}
	return e
}
func identityText(s string) bool {
	return s != "" && len(s) <= 254 && strings.IndexFunc(s, unicode.IsControl) < 0
}
func validRole(s string) bool { return s == "owner" || s == "admin" || s == "read_only" }
func validEmail(s string) bool {
	a, e := mail.ParseAddress(s)
	return identityText(s) && e == nil && a.Address == s
}
func invalidIdentity() error { return rpc.Errf("invalid_argument", "Invalid identity") }
func (v *userCreateArgs) Validate() error {
	if !identityText(v.Username) || strings.IndexFunc(v.Username, unicode.IsSpace) >= 0 || !validEmail(v.Email) || !validRole(v.Role) || v.Password == "" {
		return invalidIdentity()
	}
	return nil
}
func (v *userUpdateArgs) Validate() error {
	if !id.Valid(v.UserID) || (v.Email != nil && !validEmail(*v.Email)) || (v.Role != nil && !validRole(*v.Role)) {
		return invalidIdentity()
	}
	return nil
}
func (v *userIDArgs) Validate() error {
	if !id.Valid(v.UserID) {
		return invalidIdentity()
	}
	return nil
}
func (v *userPasswordArgs) Validate() error {
	if !id.Valid(v.UserID) || v.Password == "" {
		return invalidIdentity()
	}
	return nil
}
func userCreate(ctx context.Context, a *agent.Agent, v any) (any, error) {
	args := v.(*userCreateArgs)
	uid, e := id.New()
	if e != nil {
		return nil, e
	}
	phc := auth.Hash(args.Password)
	if phc == "" {
		return nil, rpc.Errf("agent_error", "Password hashing unavailable")
	}
	if e = a.Store.CreateUser(ctx, store.User{ID: uid, Username: args.Username, Email: args.Email, Role: args.Role, PWPHC: phc}); e != nil {
		return nil, identityError(e)
	}
	return struct {
		ID string `json:"user_id"`
	}{uid}, nil
}
func userUpdate(ctx context.Context, a *agent.Agent, v any) (any, error) {
	args := v.(*userUpdateArgs)
	return struct{}{}, identityError(a.Store.UpdateUser(ctx, args.UserID, store.UserPatch{Email: args.Email, Role: args.Role, Disabled: args.Disabled}))
}
func userDelete(ctx context.Context, a *agent.Agent, v any) (any, error) {
	return struct{}{}, identityError(a.Store.DeleteUser(ctx, v.(*userIDArgs).UserID))
}
func userSetPassword(ctx context.Context, a *agent.Agent, v any) (any, error) {
	args := v.(*userPasswordArgs)
	phc := auth.Hash(args.Password)
	if phc == "" {
		return nil, rpc.Errf("agent_error", "Password hashing unavailable")
	}
	return struct{}{}, identityError(a.Store.SetPassword(ctx, args.UserID, phc))
}

type userView struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Email    string `json:"email"`
	Role     string `json:"role"`
	Disabled bool   `json:"disabled"`
}

func userList(ctx context.Context, a *agent.Agent, _ any) (any, error) {
	rows, e := a.Store.ListUsers(ctx)
	if e != nil {
		return nil, e
	}
	out := make([]userView, 0, len(rows))
	for _, u := range rows {
		out = append(out, userView{u.ID, u.Username, u.Email, u.Role, u.Disabled})
	}
	return struct {
		Users []userView `json:"users"`
	}{out}, nil
}
