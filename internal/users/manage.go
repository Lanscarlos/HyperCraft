package users

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/lanscarlos/hypercraft/internal/auth"
	"github.com/lanscarlos/hypercraft/internal/authz"
)

// Initial builds the first users.json from the single credential a panel used
// to hold, making its owner the administrator.
//
// The same function serves a brand-new panel and an upgraded one: on a new
// panel the caller has just minted the credential, on an upgrade it came out of
// panel.json. Both end up with one administrator and the preset roles.
func Initial(cred auth.Credential) (File, error) {
	name, err := CleanUsername(cred.Username)
	if err != nil {
		// A credential written before usernames were checked this strictly, or
		// hand-edited. Refusing to start over it would lock the operator out of
		// their own panel for a cosmetic reason.
		name = "admin"
	}
	id, err := newID()
	if err != nil {
		return File{}, err
	}
	return File{
		Users: []User{{
			ID:         id,
			Username:   name,
			RoleID:     RoleAdmin,
			Credential: cred,
			CreatedAt:  time.Now(),
		}},
		Roles: presetRoles(),
	}, nil
}

// presetRoles are what a new panel starts with. See the proposal's 出厂预设.
//
// They are a starting point, not a contract: once written they are ordinary
// stored roles that an operator edits or deletes, and nothing here puts them
// back. Which is why the dangerous capabilities in 开发 are not an oversight —
// that role decides what code a server runs, and a role that could not would
// not be a developer's role. See authz.Info.Dangerous.
func presetRoles() []Role {
	ops := []authz.Cap{
		authz.CapInstanceView,
		authz.CapInstancePower,
		authz.CapInstanceConsole,
		authz.CapInstanceHistory,
		authz.CapPanelSystem,
	}
	dev := append(slices.Clone(ops),
		authz.CapInstanceConfig,
		authz.CapInstanceFilesRead,
		authz.CapInstanceFilesWrite,
		authz.CapInstancePlugins,
		authz.CapInstanceSchematics,
		authz.CapInstanceSettings,
		authz.CapInstanceLaunch,
		// A developer cannot install a plugin that is not in the library, or
		// change a core they cannot list, so the shared shelves come with the
		// role. The cost is that their changes there are visible to everybody.
		authz.CapLibraryCores,
		authz.CapLibraryPlugins,
		authz.CapLibrarySchems,
	)
	return []Role{
		{ID: RoleOps, Name: "运维", Caps: ops},
		{ID: RoleDev, Name: "开发", Caps: dev},
	}
}

// AddUser creates an account.
func (r *Registry) AddUser(username, displayName, roleID, password string, instances []string) (User, error) {
	name, err := CleanUsername(username)
	if err != nil {
		return User{}, err
	}
	cred, err := auth.NewCredential(name, password)
	if err != nil {
		return User{}, err
	}
	id, err := newID()
	if err != nil {
		return User{}, err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, taken := r.byName[strings.ToLower(name)]; taken {
		return User{}, ErrDuplicateName
	}
	if err := r.roleExistsLocked(roleID); err != nil {
		return User{}, err
	}

	u := User{
		ID:          id,
		Username:    name,
		DisplayName: strings.TrimSpace(displayName),
		RoleID:      roleID,
		Instances:   normaliseGrant(roleID, instances),
		Credential:  cred,
		CreatedAt:   time.Now(),
	}
	r.users = append(r.users, u)
	r.reindex()
	return u, nil
}

// Edit is the editable part of an account. Password is not in it: changing one
// is its own act, with its own audit line and its own session revocation.
type Edit struct {
	Username    string
	DisplayName string
	RoleID      string
	// Instances is the server grant; nil means every server. See User.Instances.
	Instances []string
	Disabled  bool
}

// UpdateUser applies an edit, refusing the two that would lock everybody out:
// taking the role off the last administrator, and switching them off.
func (r *Registry) UpdateUser(id string, edit Edit) (User, error) {
	name, err := CleanUsername(edit.Username)
	if err != nil {
		return User{}, err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	idx, ok := r.byID[id]
	if !ok {
		return User{}, ErrNotFound
	}
	if taken, exists := r.byName[strings.ToLower(name)]; exists && taken != idx {
		return User{}, ErrDuplicateName
	}
	if err := r.roleExistsLocked(edit.RoleID); err != nil {
		return User{}, err
	}

	stillAdmin := edit.RoleID == RoleAdmin && !edit.Disabled
	if !stillAdmin && r.lastEnabledAdminLocked(id) {
		return User{}, ErrLastAdmin
	}

	u := r.users[idx]
	u.Username = name
	u.DisplayName = strings.TrimSpace(edit.DisplayName)
	u.RoleID = edit.RoleID
	u.Instances = normaliseGrant(edit.RoleID, edit.Instances)
	u.Disabled = edit.Disabled
	r.users[idx] = u
	r.reindex()
	return u, nil
}

// SetPassword replaces an account's password. The credential is re-derived
// under the account's current username, so the record inside it does not drift
// further than the last rename.
func (r *Registry) SetPassword(id, password string) (User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	idx, ok := r.byID[id]
	if !ok {
		return User{}, ErrNotFound
	}
	cred, err := auth.NewCredential(r.users[idx].Username, password)
	if err != nil {
		return User{}, err
	}
	r.users[idx].Credential = cred
	return r.users[idx], nil
}

// DeleteUser removes an account.
func (r *Registry) DeleteUser(id string) (User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	idx, ok := r.byID[id]
	if !ok {
		return User{}, ErrNotFound
	}
	if r.lastEnabledAdminLocked(id) {
		return User{}, ErrLastAdmin
	}
	u := r.users[idx]
	r.users = slices.Delete(r.users, idx, idx+1)
	r.reindex()
	return u, nil
}

// lastEnabledAdminLocked reports whether id is the only enabled administrator
// left, which is the state that must survive every edit: a panel with no way in
// is not recoverable from the panel.
func (r *Registry) lastEnabledAdminLocked(id string) bool {
	self := false
	others := 0
	for _, u := range r.users {
		if u.RoleID != RoleAdmin || u.Disabled {
			continue
		}
		if u.ID == id {
			self = true
			continue
		}
		others++
	}
	return self && others == 0
}

func (r *Registry) roleExistsLocked(roleID string) error {
	if roleID == RoleAdmin {
		return nil
	}
	for _, role := range r.roles {
		if role.ID == roleID {
			return nil
		}
	}
	return ErrUnknownRole
}

// ---------------------------------------------------------------- roles

// AddRole creates a role.
func (r *Registry) AddRole(name string, caps []authz.Cap) (Role, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Role{}, ErrRoleNameInvalid
	}
	kept, err := cleanCaps(caps)
	if err != nil {
		return Role{}, err
	}
	id, err := newID()
	if err != nil {
		return Role{}, err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	role := Role{ID: id, Name: name, Caps: kept}
	r.roles = append(r.roles, role)
	return role, nil
}

// UpdateRole replaces a role's name and capabilities. Every account holding it
// is affected at once, which is the point of roles existing.
func (r *Registry) UpdateRole(id, name string, caps []authz.Cap) (Role, error) {
	if slices.Contains(reservedRoleIDs, id) {
		return Role{}, ErrReservedRole
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return Role{}, ErrRoleNameInvalid
	}
	kept, err := cleanCaps(caps)
	if err != nil {
		return Role{}, err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	for i, role := range r.roles {
		if role.ID != id {
			continue
		}
		r.roles[i].Name = name
		r.roles[i].Caps = kept
		return r.roles[i], nil
	}
	return Role{}, ErrNotFound
}

// DeleteRole removes a role, refusing while an account still points at it —
// that account would otherwise be left holding nothing, which looks like a
// permission bug rather than a deletion.
func (r *Registry) DeleteRole(id string) (Role, error) {
	if slices.Contains(reservedRoleIDs, id) {
		return Role{}, ErrReservedRole
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	for _, u := range r.users {
		if u.RoleID == id {
			return Role{}, fmt.Errorf("%w（%s）", ErrRoleInUse, u.Username)
		}
	}
	for i, role := range r.roles {
		if role.ID == id {
			r.roles = slices.Delete(r.roles, i, i+1)
			return role, nil
		}
	}
	return Role{}, ErrNotFound
}

// cleanCaps drops duplicates and refuses anything outside the vocabulary. An
// unknown capability is a typo or a downgrade, and storing it would mean a role
// that silently grants less than its editor believes it does.
func cleanCaps(caps []authz.Cap) ([]authz.Cap, error) {
	kept := make([]authz.Cap, 0, len(caps))
	for _, cap := range caps {
		if !authz.Valid(cap) {
			return nil, fmt.Errorf("%q 不是有效的能力", cap)
		}
		if !slices.Contains(kept, cap) {
			kept = append(kept, cap)
		}
	}
	return kept, nil
}

// normaliseGrant cleans a server grant, and forces an administrator's to nil.
//
// Restricting an administrator would be theatre: they can edit their own grant.
// Worse, it would be theatre that locks somebody out of a server while leaving
// them able to unlock it, which reads as a bug from both sides.
func normaliseGrant(roleID string, instances []string) []string {
	if roleID == RoleAdmin || instances == nil {
		return nil
	}
	kept := make([]string, 0, len(instances))
	for _, id := range instances {
		id = strings.TrimSpace(id)
		if id != "" && !slices.Contains(kept, id) {
			kept = append(kept, id)
		}
	}
	return kept
}

// ByUsername finds an account by login name, case-insensitively. Used by the
// command line, which is handed a name rather than an id; requests resolve
// accounts by id — see ByID.
func (r *Registry) ByUsername(name string) (User, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	idx, ok := r.byName[strings.ToLower(strings.TrimSpace(name))]
	if !ok {
		return User{}, false
	}
	return r.users[idx], true
}

// FirstAdmin returns the oldest enabled administrator, which is who
// -reset-password falls back to when it is not told a name.
func (r *Registry) FirstAdmin() (User, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, u := range r.users {
		if u.RoleID == RoleAdmin && !u.Disabled {
			return u, true
		}
	}
	return User{}, false
}
