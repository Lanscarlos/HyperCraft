// Package users owns the panel's accounts and the roles they are given.
//
// It is the runtime authority on who exists; store just parks the list in
// users.json between runs, the way config.Panel parks the device list.
//
// What lives here and what does not: this package answers "who is this" and
// "what may they do", as a set of capabilities. It does not answer "to which
// servers" — instance scoping is a second dimension, and until it lands every
// account reaches every instance. See docs/proposal-multi-user.md.
package users

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/lanscarlos/hypercraft/internal/auth"
	"github.com/lanscarlos/hypercraft/internal/authz"
)

// RoleAdmin is the built-in role. It is not stored, cannot be edited or
// deleted, and its capability set is the whole vocabulary by definition — so a
// capability added in a later release is one an administrator already has,
// rather than one every existing panel silently lacks.
const RoleAdmin = "admin"

// The preset roles a new panel starts with. They are ordinary stored roles from
// the moment they are written: an operator may edit or delete them, and nothing
// puts them back. The ids are readable rather than random so a hand-edited
// users.json has something to name.
const (
	RoleOps = "ops"
	RoleDev = "dev"
)

// reservedRoleIDs can never be taken by a role an operator creates.
var reservedRoleIDs = []string{RoleAdmin}

const maxNameLen = 64

var (
	ErrNotFound        = errors.New("no such account")
	ErrDuplicateName   = errors.New("用户名已被占用")
	ErrUnknownRole     = errors.New("角色不存在")
	ErrReservedRole    = errors.New("这个角色是内置的，不能修改或删除")
	ErrRoleInUse       = errors.New("还有账号在用这个角色")
	ErrLastAdmin       = errors.New("至少要保留一个启用中的管理员")
	ErrNameRequired    = errors.New("用户名不能为空")
	ErrNameTooLong     = fmt.Errorf("用户名不能超过 %d 个字符", maxNameLen)
	ErrNameInvalid     = errors.New("用户名只能包含字母、数字、下划线、点和减号")
	ErrRoleNameInvalid = errors.New("角色名不能为空")
)

// User is one account.
type User struct {
	// ID never changes, which is what lets a username be edited without
	// stranding the device tokens and (later) the instance grants pointing at
	// this account.
	ID string `json:"id"`
	// Username is what gets typed at the login form. Authoritative — the copy
	// inside Credential is only a record of what the password was derived
	// under. Compared case-insensitively for uniqueness, stored as typed.
	Username string `json:"username"`
	// DisplayName is the operator's own label, shown instead of the username
	// where there is room. Optional.
	DisplayName string          `json:"displayName,omitempty"`
	RoleID      string          `json:"roleId"`
	Credential  auth.Credential `json:"credential"`
	// Instances are the servers this account may touch, by id.
	//
	// nil means every server, including ones created later — which is what an
	// administrator wants and what every account had before scoping existed. An
	// empty list means none at all, and the two have to stay distinguishable:
	// no omitempty here, so the field is written as null or [] rather than
	// disappearing and reading back as "everything".
	//
	// It narrows capabilities rather than granting anything. An account with no
	// instance capabilities reaches nothing whatever this says.
	Instances []string `json:"instances"`
	// Disabled keeps the account and its history while refusing every login.
	// Deleting is the other option, and it is not the same one: an account that
	// has been switched off can be switched back on.
	Disabled  bool      `json:"disabled,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
}

// Role is a named set of capabilities.
type Role struct {
	ID   string      `json:"id"`
	Name string      `json:"name"`
	Caps []authz.Cap `json:"capabilities"`
	// Paths confines this role's file manager to part of each instance
	// directory — "plugins/MyPlugin" and nothing else. Empty means the whole
	// directory, which is what every role had before this existed.
	//
	// It narrows the two file capabilities and nothing else. A role that can
	// install a plugin or change the launch command reaches the whole machine
	// and this list with it; the role editor says so out loud rather than
	// leaving an operator to work it out. See docs/security.md.
	Paths []string `json:"paths,omitempty"`
}

// File is the on-disk shape of users.json.
type File struct {
	Users []User `json:"users"`
	Roles []Role `json:"roles"`
}

// Registry holds the accounts. Every read and write goes through it.
type Registry struct {
	mu    sync.RWMutex
	users []User
	roles []Role
	// byID and byName index the slices; both are rebuilt on every mutation,
	// which costs nothing at the scale this holds (a team, not a user table).
	byID   map[string]int
	byName map[string]int
	// decoy is a real credential over a password nobody knows. Authenticate
	// runs it when the username does not exist, so a probe cannot tell "no
	// such account" from "wrong password" by how long the answer took — the
	// derivation is a tenth of a second and skipping it would be obvious.
	decoy auth.Credential
}

// New builds a registry from a loaded file.
//
// It repairs rather than refuses: users.json is a file the operator is invited
// to read, so a hand-edited entry is possible, and a panel that will not start
// because one row is malformed is a panel that has locked its owner out. Broken
// rows are dropped and unknown capabilities ignored; the caller logs what went
// missing.
func New(file File) (*Registry, []string, error) {
	decoyPassword, err := auth.GeneratePassword()
	if err != nil {
		return nil, nil, fmt.Errorf("build decoy credential: %w", err)
	}
	decoy, err := auth.NewCredential("decoy", decoyPassword)
	if err != nil {
		return nil, nil, fmt.Errorf("build decoy credential: %w", err)
	}

	r := &Registry{decoy: decoy}
	var complaints []string

	seenRole := make(map[string]bool)
	for _, role := range file.Roles {
		switch {
		case role.ID == "" || seenRole[role.ID]:
			complaints = append(complaints, fmt.Sprintf("角色 %q 的 id 为空或重复，已忽略", role.Name))
			continue
		case slices.Contains(reservedRoleIDs, role.ID):
			complaints = append(complaints, fmt.Sprintf("角色 id %q 是内置的，已忽略", role.ID))
			continue
		}
		kept := make([]authz.Cap, 0, len(role.Caps))
		for _, cap := range role.Caps {
			if !authz.Valid(cap) {
				complaints = append(complaints, fmt.Sprintf("角色 %q 里的 %q 不是这个版本认识的能力，已忽略", role.Name, cap))
				continue
			}
			if !slices.Contains(kept, cap) {
				kept = append(kept, cap)
			}
		}
		role.Caps = kept
		role.Paths = cleanPaths(role.Paths)
		seenRole[role.ID] = true
		r.roles = append(r.roles, role)
	}

	seenName := make(map[string]bool)
	for _, u := range file.Users {
		name := strings.ToLower(strings.TrimSpace(u.Username))
		switch {
		case u.ID == "":
			complaints = append(complaints, fmt.Sprintf("账号 %q 没有 id，已忽略", u.Username))
			continue
		case name == "" || seenName[name]:
			complaints = append(complaints, fmt.Sprintf("账号 %q 的用户名为空或重复，已忽略", u.Username))
			continue
		case u.Credential.IsZero():
			complaints = append(complaints, fmt.Sprintf("账号 %q 没有密码，已忽略", u.Username))
			continue
		}
		if u.RoleID != RoleAdmin && !seenRole[u.RoleID] {
			// Falling back to admin would hand out the panel; refusing to load
			// the account is the safe direction to fail in.
			complaints = append(complaints, fmt.Sprintf("账号 %q 指向的角色 %q 不存在，已忽略", u.Username, u.RoleID))
			continue
		}
		seenName[name] = true
		r.users = append(r.users, u)
	}

	r.reindex()
	return r, complaints, nil
}

func (r *Registry) reindex() {
	r.byID = make(map[string]int, len(r.users))
	r.byName = make(map[string]int, len(r.users))
	for i, u := range r.users {
		r.byID[u.ID] = i
		r.byName[strings.ToLower(u.Username)] = i
	}
}

// Snapshot returns the state to write to disk.
func (r *Registry) Snapshot() File {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return File{Users: slices.Clone(r.users), Roles: slices.Clone(r.roles)}
}

// Authenticate checks a login.
//
// Every failure returns the same error and, as far as the clock is concerned,
// takes the same time: an unknown username, a disabled account and a wrong
// password are indistinguishable from outside. The panel's login form is
// public, so "which names exist" is worth exactly as much to an attacker as
// the first half of a password.
func (r *Registry) Authenticate(username, password string) (User, error) {
	r.mu.RLock()
	idx, ok := r.byName[strings.ToLower(strings.TrimSpace(username))]
	var u User
	if ok {
		u = r.users[idx]
	}
	decoy := r.decoy
	r.mu.RUnlock()

	if !ok || u.Disabled {
		_ = decoy.VerifyPassword(password)
		return User{}, auth.ErrInvalidCredentials
	}
	if err := u.Credential.VerifyPassword(password); err != nil {
		return User{}, auth.ErrInvalidCredentials
	}
	return u, nil
}

// ByID returns an account. Handlers resolve the caller this way on every
// request rather than trusting a name carried in the session, so deleting or
// disabling an account takes effect on its next request instead of whenever its
// session happens to expire.
func (r *Registry) ByID(id string) (User, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	idx, ok := r.byID[id]
	if !ok {
		return User{}, false
	}
	return r.users[idx], true
}

// List returns every account, in creation order.
func (r *Registry) List() []User {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return slices.Clone(r.users)
}

// Roles returns the built-in role followed by the stored ones.
func (r *Registry) Roles() []Role {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Role, 0, len(r.roles)+1)
	out = append(out, adminRole())
	return append(out, r.roles...)
}

// adminRole is synthesised rather than stored, so "the administrator has
// everything" stays true across a release that adds a capability.
func adminRole() Role {
	caps := make([]authz.Cap, 0, len(authz.All()))
	for _, info := range authz.All() {
		caps = append(caps, info.Cap)
	}
	return Role{ID: RoleAdmin, Name: "管理员", Caps: caps}
}

// Capabilities resolves what an account may do.
func (r *Registry) Capabilities(u User) map[authz.Cap]bool {
	if u.RoleID == RoleAdmin {
		out := make(map[authz.Cap]bool, len(authz.All()))
		for _, info := range authz.All() {
			out[info.Cap] = true
		}
		return out
	}

	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, role := range r.roles {
		if role.ID != u.RoleID {
			continue
		}
		out := make(map[authz.Cap]bool, len(role.Caps))
		for _, cap := range role.Caps {
			out[cap] = true
		}
		return out
	}
	// A role that vanished under an account grants nothing. New() refuses to
	// load such an account, so this is only reachable if a role is deleted
	// while a request is in flight.
	return map[authz.Cap]bool{}
}

// IsAdmin reports whether an account holds the built-in role.
func (r *Registry) IsAdmin(u User) bool { return u.RoleID == RoleAdmin }

// PathsFor returns the file-manager confinement an account's role carries, or
// nothing at all for an unrestricted one. Administrators are never confined.
func (r *Registry) PathsFor(u User) []string {
	if u.RoleID == RoleAdmin {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, role := range r.roles {
		if role.ID == u.RoleID {
			return slices.Clone(role.Paths)
		}
	}
	// A role that vanished under an account. New refuses to load one, so this
	// is only reachable mid-request — and confining to nothing is the safe
	// direction, since the capabilities resolve to empty in the same case.
	return []string{}
}

// cleanPaths normalises a confinement list: trimmed, slash-separated, no
// leading or trailing slash, no duplicates, and nothing that means the whole
// instance.
//
// Dropping a prefix that means everything rather than honouring it is the
// important part. "/" cleans to the instance root, and a rule that silently
// grants the whole directory is worse than no rule — the operator who typed it
// believes they restricted something.
func cleanPaths(paths []string) []string {
	kept := make([]string, 0, len(paths))
	for _, raw := range paths {
		p := strings.Trim(strings.TrimSpace(strings.ReplaceAll(raw, "\\", "/")), "/")
		if p == "" || p == "." || strings.Contains(p, "\x00") {
			continue
		}
		if p != path.Clean(p) || p == ".." || strings.HasPrefix(p, "../") {
			continue
		}
		if !slices.Contains(kept, p) {
			kept = append(kept, p)
		}
	}
	return kept
}

// CanUseInstance reports whether an account's grant covers one server.
//
// It answers "which servers", never "what may be done to them" — a caller has
// to ask both. Administrators are always covered: their grant is forced to nil
// when the account is written, because an administrator can edit their own
// grant anyway and a restricted one would be theatre with a lockout attached.
func (r *Registry) CanUseInstance(u User, instanceID string) bool {
	if u.RoleID == RoleAdmin || u.Instances == nil {
		return true
	}
	return slices.Contains(u.Instances, instanceID)
}

// GrantInstance adds one server to an account's grant, and reports whether it
// had to. A no-op for an account that already reaches everything.
//
// It exists for one case: somebody who may create servers but only sees some of
// them would otherwise create one and immediately lose sight of it.
func (r *Registry) GrantInstance(id, instanceID string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	idx, ok := r.byID[id]
	if !ok {
		return false, ErrNotFound
	}
	u := r.users[idx]
	if u.Instances == nil || slices.Contains(u.Instances, instanceID) {
		return false, nil
	}
	r.users[idx].Instances = append(slices.Clone(u.Instances), instanceID)
	return true, nil
}

// ForgetInstance drops a deleted server from every grant, so an id cannot be
// inherited by whatever is created next. Reports how many accounts changed.
func (r *Registry) ForgetInstance(instanceID string) int {
	r.mu.Lock()
	defer r.mu.Unlock()

	n := 0
	for i, u := range r.users {
		if u.Instances == nil {
			continue
		}
		kept := slices.DeleteFunc(slices.Clone(u.Instances), func(id string) bool {
			return id == instanceID
		})
		if len(kept) != len(u.Instances) {
			r.users[i].Instances = kept
			n++
		}
	}
	return n
}

func newID() (string, error) {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate id: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// CleanUsername normalises and checks a login name. The character set is
// deliberately narrow: a username is typed at a login form, read back out of a
// log line, and compared case-insensitively, and none of that goes well with
// spaces or right-to-left marks.
func CleanUsername(name string) (string, error) {
	name = strings.TrimSpace(name)
	switch {
	case name == "":
		return "", ErrNameRequired
	case utf8.RuneCountInString(name) > maxNameLen:
		return "", ErrNameTooLong
	}
	for _, c := range name {
		if c > unicode.MaxASCII || (!unicode.IsLetter(c) && !unicode.IsDigit(c) && !strings.ContainsRune("_.-", c)) {
			return "", ErrNameInvalid
		}
	}
	return name, nil
}
