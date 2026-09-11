package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/lanscarlos/hypercraft/internal/authz"
	"github.com/lanscarlos/hypercraft/internal/users"
)

// The accounts and roles pages.
//
// Everything here sits behind authz.CapPanelUsers, which is a dangerous
// capability for the obvious reason: an account that can edit roles can put
// itself in one that holds everything. That is not a flaw to be designed
// around — somebody has to be able to hand out permissions — but it is why the
// capability is flagged, and why these handlers refuse the one edit that is not
// recoverable: leaving the panel with no enabled administrator.

type accountResponse struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"displayName,omitempty"`
	RoleID      string `json:"roleId"`
	RoleName    string `json:"roleName"`
	Disabled    bool   `json:"disabled"`
	// Devices counts this account's pairings, so the page can say what
	// disabling or deleting it will cut off.
	Devices   int       `json:"devices"`
	CreatedAt time.Time `json:"createdAt"`
	// Self marks the caller's own row, which the UI warns before touching:
	// changing your own role is the one edit here that can lock you out of the
	// page you are standing on.
	Self bool `json:"self"`
}

type roleResponse struct {
	ID           string      `json:"id"`
	Name         string      `json:"name"`
	Capabilities []authz.Cap `json:"capabilities"`
	// BuiltIn marks the administrator role: it holds the whole vocabulary by
	// definition and cannot be edited or deleted.
	BuiltIn bool `json:"builtIn"`
	// Users counts the accounts holding it, which is what makes "delete" refuse
	// and what the editor shows before a change lands on several people at once.
	Users int `json:"users"`
}

type capabilityResponse struct {
	ID        authz.Cap   `json:"id"`
	Title     string      `json:"title"`
	Note      string      `json:"note,omitempty"`
	Scope     authz.Scope `json:"scope"`
	Dangerous bool        `json:"dangerous"`
}

func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	who, _ := principalFrom(r.Context())
	writeJSON(w, http.StatusOK, s.describeAccounts(who))
}

func (s *Server) describeAccounts(who principal) []accountResponse {
	names := make(map[string]string)
	for _, role := range s.accounts.Roles() {
		names[role.ID] = role.Name
	}

	list := s.accounts.List()
	out := make([]accountResponse, 0, len(list))
	for _, u := range list {
		out = append(out, accountResponse{
			ID:          u.ID,
			Username:    u.Username,
			DisplayName: u.DisplayName,
			RoleID:      u.RoleID,
			RoleName:    names[u.RoleID],
			Disabled:    u.Disabled,
			Devices:     len(s.devices.ListUser(u.ID)),
			CreatedAt:   u.CreatedAt,
			Self:        u.ID == who.user.ID,
		})
	}
	return out
}

type createUserRequest struct {
	Username    string `json:"username"`
	DisplayName string `json:"displayName"`
	RoleID      string `json:"roleId"`
	Password    string `json:"password"`
}

func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	var req createUserRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}

	// Deriving a password is the same tenth of a second here as at the login
	// form, so this endpoint goes through the same gate. It is behind
	// requireAuth, but an administrator holding a script is still a way to
	// spend the CPU the Minecraft servers on this machine are not getting.
	if !s.kdf.enter(r.Context()) {
		writeBusy(w)
		return
	}
	user, err := s.accounts.AddUser(req.Username, req.DisplayName, req.RoleID, req.Password)
	s.kdf.leave()
	if err != nil {
		s.writeUsersError(w, err)
		return
	}

	if err := s.persistUsers(); err != nil {
		// Never leave an account behind that the panel failed to record: it
		// would work until the next restart and then vanish.
		_, _ = s.accounts.DeleteUser(user.ID)
		s.writeDomainError(w, err)
		return
	}

	who, _ := principalFrom(r.Context())
	s.log.Info("account created", "username", user.Username, "role", user.RoleID, "by", who.username())
	s.recordAuth(r, eventUserCreated, who.username(), user.Username)
	writeJSON(w, http.StatusCreated, s.describeAccounts(who))
}

type updateUserRequest struct {
	Username    string `json:"username"`
	DisplayName string `json:"displayName"`
	RoleID      string `json:"roleId"`
	Disabled    bool   `json:"disabled"`
}

func (s *Server) handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	var req updateUserRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}

	id := r.PathValue("id")
	before, ok := s.accounts.ByID(id)
	if !ok {
		writeError(w, http.StatusNotFound, "账号不存在")
		return
	}

	user, err := s.accounts.UpdateUser(id, users.Edit{
		Username:    req.Username,
		DisplayName: req.DisplayName,
		RoleID:      req.RoleID,
		Disabled:    req.Disabled,
	})
	if err != nil {
		s.writeUsersError(w, err)
		return
	}

	// Switching an account off drops its sessions now rather than leaving them
	// to expire. requireAuth would refuse them anyway — it re-reads the account
	// on every request — so this is tidiness rather than the barrier, and the
	// barrier is the one that matters.
	if user.Disabled && !before.Disabled {
		s.sessions.RevokeUser(id)
	}

	if err := s.persistUsers(); err != nil {
		s.writeDomainError(w, err)
		return
	}

	who, _ := principalFrom(r.Context())
	s.log.Info("account updated", "username", user.Username, "role", user.RoleID,
		"disabled", user.Disabled, "by", who.username())
	s.recordAuth(r, eventUserUpdated, who.username(), user.Username)
	writeJSON(w, http.StatusOK, s.describeAccounts(who))
}

type setPasswordRequest struct {
	Password string `json:"password"`
}

// handleSetUserPassword is an administrator resetting somebody else's password.
// It deliberately does not ask for the administrator's own password the way
// handleChangePassword does: the caller already holds a capability that can
// grant itself anything, so a second prompt would buy nothing but the habit of
// typing your password into a box.
func (s *Server) handleSetUserPassword(w http.ResponseWriter, r *http.Request) {
	var req setPasswordRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}

	id := r.PathValue("id")
	if !s.kdf.enter(r.Context()) {
		writeBusy(w)
		return
	}
	user, err := s.accounts.SetPassword(id, req.Password)
	s.kdf.leave()
	if err != nil {
		s.writeUsersError(w, err)
		return
	}

	// The same reasoning as a self-service password change: every credential
	// minted under the old password stops working.
	s.sessions.RevokeUser(id)
	unpaired := s.devices.RevokeUser(id)

	if err := s.persistUsers(); err != nil {
		s.writeDomainError(w, err)
		return
	}
	if unpaired > 0 {
		if err := s.persistPanel(); err != nil {
			s.writeDomainError(w, err)
			return
		}
	}

	who, _ := principalFrom(r.Context())
	s.log.Info("account password reset", "username", user.Username, "by", who.username(), "devicesUnpaired", unpaired)
	s.recordAuth(r, eventPasswordChanged, who.username(), user.Username)
	w.WriteHeader(http.StatusNoContent)
}

// handleSignOutUser drops every session and pairing an account holds, without
// touching its password. The button for "somebody walked off with a signed-in
// laptop".
func (s *Server) handleSignOutUser(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	user, ok := s.accounts.ByID(id)
	if !ok {
		writeError(w, http.StatusNotFound, "账号不存在")
		return
	}

	sessions := s.sessions.RevokeUser(id)
	unpaired := s.devices.RevokeUser(id)
	if unpaired > 0 {
		if err := s.persistPanel(); err != nil {
			s.writeDomainError(w, err)
			return
		}
	}

	who, _ := principalFrom(r.Context())
	s.log.Info("account signed out", "username", user.Username, "by", who.username(),
		"sessions", sessions, "devicesUnpaired", unpaired)
	s.recordAuth(r, eventUnpaired, who.username(), user.Username)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	user, err := s.accounts.DeleteUser(id)
	if err != nil {
		s.writeUsersError(w, err)
		return
	}

	// The account is gone, so its credentials have to go with it. Order
	// matters: they are dropped before the write, so the file that lands on
	// disk already reflects a panel this account cannot reach.
	s.sessions.RevokeUser(id)
	unpaired := s.devices.RevokeUser(id)

	if err := s.persistUsers(); err != nil {
		s.writeDomainError(w, err)
		return
	}
	if unpaired > 0 {
		if err := s.persistPanel(); err != nil {
			s.writeDomainError(w, err)
			return
		}
	}

	who, _ := principalFrom(r.Context())
	s.log.Info("account deleted", "username", user.Username, "by", who.username(), "devicesUnpaired", unpaired)
	s.recordAuth(r, eventUserDeleted, who.username(), user.Username)
	w.WriteHeader(http.StatusNoContent)
}

// ---------------------------------------------------------------- roles

func (s *Server) handleListRoles(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.describeRoles())
}

func (s *Server) describeRoles() []roleResponse {
	counts := make(map[string]int)
	for _, u := range s.accounts.List() {
		counts[u.RoleID]++
	}

	roles := s.accounts.Roles()
	out := make([]roleResponse, 0, len(roles))
	for _, role := range roles {
		out = append(out, roleResponse{
			ID:           role.ID,
			Name:         role.Name,
			Capabilities: role.Caps,
			BuiltIn:      role.ID == users.RoleAdmin,
			Users:        counts[role.ID],
		})
	}
	return out
}

type roleRequest struct {
	Name         string      `json:"name"`
	Capabilities []authz.Cap `json:"capabilities"`
}

func (s *Server) handleCreateRole(w http.ResponseWriter, r *http.Request) {
	var req roleRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}

	role, err := s.accounts.AddRole(req.Name, req.Capabilities)
	if err != nil {
		s.writeUsersError(w, err)
		return
	}
	if err := s.persistUsers(); err != nil {
		_, _ = s.accounts.DeleteRole(role.ID)
		s.writeDomainError(w, err)
		return
	}

	who, _ := principalFrom(r.Context())
	s.log.Info("role created", "role", role.Name, "capabilities", len(role.Caps), "by", who.username())
	writeJSON(w, http.StatusCreated, s.describeRoles())
}

func (s *Server) handleUpdateRole(w http.ResponseWriter, r *http.Request) {
	var req roleRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}

	role, err := s.accounts.UpdateRole(r.PathValue("id"), req.Name, req.Capabilities)
	if err != nil {
		s.writeUsersError(w, err)
		return
	}
	if err := s.persistUsers(); err != nil {
		s.writeDomainError(w, err)
		return
	}

	who, _ := principalFrom(r.Context())
	s.log.Info("role updated", "role", role.Name, "capabilities", len(role.Caps), "by", who.username())
	writeJSON(w, http.StatusOK, s.describeRoles())
}

func (s *Server) handleDeleteRole(w http.ResponseWriter, r *http.Request) {
	role, err := s.accounts.DeleteRole(r.PathValue("id"))
	if err != nil {
		s.writeUsersError(w, err)
		return
	}
	if err := s.persistUsers(); err != nil {
		s.writeDomainError(w, err)
		return
	}

	who, _ := principalFrom(r.Context())
	s.log.Info("role deleted", "role", role.Name, "by", who.username())
	w.WriteHeader(http.StatusNoContent)
}

// handleCapabilities serves the vocabulary the role editor is built from, so
// the front end never carries its own copy of the capability list. A capability
// added in a later release shows up in the editor without a front-end change.
func (s *Server) handleCapabilities(w http.ResponseWriter, _ *http.Request) {
	all := authz.All()
	out := make([]capabilityResponse, 0, len(all))
	for _, info := range all {
		out = append(out, capabilityResponse{
			ID:        info.Cap,
			Title:     info.Title,
			Note:      info.Note,
			Scope:     info.Scope,
			Dangerous: info.Dangerous,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// writeUsersError maps the registry's refusals onto status codes. They are all
// things an operator did rather than things that went wrong, so the message is
// passed through: "至少要保留一个启用中的管理员" is the whole explanation, and
// a generic 400 would throw it away.
func (s *Server) writeUsersError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, users.ErrNotFound):
		writeError(w, http.StatusNotFound, "账号或角色不存在")
	case errors.Is(err, users.ErrDuplicateName):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, users.ErrLastAdmin), errors.Is(err, users.ErrRoleInUse), errors.Is(err, users.ErrReservedRole):
		writeError(w, http.StatusConflict, err.Error())
	default:
		writeError(w, http.StatusBadRequest, err.Error())
	}
}
