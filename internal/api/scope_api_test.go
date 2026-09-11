package api

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/lanscarlos/hypercraft/internal/authz"
	"github.com/lanscarlos/hypercraft/internal/instance"
	"github.com/lanscarlos/hypercraft/internal/users"
)

// grantOnly gives one account a grant covering exactly the named servers, and
// signs in as it.
func (e *testEnv) grantOnly(username, role string, instanceIDs ...string) *testEnv {
	e.t.Helper()

	// A variadic called with no arguments gives a nil slice, and nil is the one
	// value that means "every server" — so an empty grant has to be spelled out.
	if instanceIDs == nil {
		instanceIDs = []string{}
	}

	member := e.asMember(username, role)
	account, ok := e.accounts.ByUsername(username)
	if !ok {
		e.t.Fatalf("the account %q is missing", username)
	}
	if _, err := e.accounts.UpdateUser(account.ID, users.Edit{
		Username: username, RoleID: role, Instances: instanceIDs,
	}); err != nil {
		e.t.Fatalf("UpdateUser: %v", err)
	}
	return member
}

// TestGrantScopesInstanceRoutes is the first half of scoping: a server outside
// the grant is not there as far as this account is concerned.
func TestGrantScopesInstanceRoutes(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	mine := env.createInstance("mine")
	theirs := env.createInstance("theirs")

	ops := env.grantOnly("ops", users.RoleOps, mine.ID)

	for _, path := range []string{"", "/logs", "/properties", "/metrics", "/configs"} {
		if got := ops.status(http.MethodGet, "/api/instances/"+mine.ID+path, nil); got != http.StatusOK {
			t.Errorf("GET granted instance%s: %d, want 200", path, got)
		}
		// 404 rather than 403: telling somebody that an id they may not use is
		// nonetheless a real server is the thing a grant exists not to do.
		if got := ops.status(http.MethodGet, "/api/instances/"+theirs.ID+path, nil); got != http.StatusNotFound {
			t.Errorf("GET ungranted instance%s: %d, want 404", path, got)
		}
	}
	// Including the ones that do something.
	if got := ops.status(http.MethodPost, "/api/instances/"+theirs.ID+"/stop", nil); got != http.StatusNotFound {
		t.Errorf("stopping an ungranted instance: %d, want 404", got)
	}
}

// The second half: an ungranted server must not appear in any list either,
// which is where scoping actually tends to leak.
func TestGrantFiltersTheListings(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	mine := env.createInstance("mine")
	env.createInstance("theirs")

	ops := env.grantOnly("ops", users.RoleOps, mine.ID)

	var listed []instance.Status
	decodeBody(t, ops.do(http.MethodGet, "/api/instances", nil), &listed)
	if len(listed) != 1 || listed[0].ID != mine.ID {
		t.Errorf("GET /api/instances returned %d servers, want only the granted one", len(listed))
	}

	// The dashboard's own count comes from the same place.
	var system struct {
		Instances instanceCounts `json:"instances"`
	}
	decodeBody(t, ops.do(http.MethodGet, "/api/system", nil), &system)
	if system.Instances.Total != 1 {
		t.Errorf("GET /api/system counted %d servers, want 1", system.Instances.Total)
	}

	// And the administrator still sees both, so the filter is a filter and not
	// a break.
	decodeBody(t, env.do(http.MethodGet, "/api/instances", nil), &listed)
	if len(listed) != 2 {
		t.Errorf("the administrator sees %d servers, want 2", len(listed))
	}
}

// An empty grant is not the same as an absent one. Absent means everything,
// which is what every account had before scoping existed.
func TestEmptyGrantReachesNothingAndNilGrantReachesEverything(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	env.createInstance("one")

	none := env.grantOnly("none", users.RoleOps)
	var listed []instance.Status
	decodeBody(t, none.do(http.MethodGet, "/api/instances", nil), &listed)
	if len(listed) != 0 {
		t.Errorf("an empty grant sees %d servers, want none", len(listed))
	}

	all := env.asMember("all", users.RoleOps)
	decodeBody(t, all.do(http.MethodGet, "/api/instances", nil), &listed)
	if len(listed) != 1 {
		t.Errorf("an account with no grant set sees %d servers, want all 1 of them", len(listed))
	}
}

// An administrator's grant is forced to nil when written: restricting one would
// be theatre, since they can edit their own grant.
func TestAdminGrantCannotBeNarrowed(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	mine := env.createInstance("mine")

	admin, _ := env.accounts.FirstAdmin()
	updated, err := env.accounts.UpdateUser(admin.ID, users.Edit{
		Username: admin.Username, RoleID: users.RoleAdmin, Instances: []string{mine.ID},
	})
	if err != nil {
		t.Fatalf("UpdateUser: %v", err)
	}
	if updated.Instances != nil {
		t.Errorf("the administrator's grant was narrowed to %v", updated.Instances)
	}
}

// Creating a server must not lose it: an account whose grant is a list would
// otherwise create one and immediately be unable to see it.
func TestCreatingAnInstanceGrantsItToItsCreator(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	existing := env.createInstance("existing")

	role, err := env.accounts.AddRole("建服的", []authz.Cap{authz.CapInstanceView, authz.CapPanelCreate}, nil)
	if err != nil {
		t.Fatalf("AddRole: %v", err)
	}
	member := env.grantOnly("builder", role.ID, existing.ID)

	created := member.createInstance("new one")
	var listed []instance.Status
	decodeBody(t, member.do(http.MethodGet, "/api/instances", nil), &listed)
	if len(listed) != 2 {
		t.Fatalf("the creator sees %d servers, want the one granted plus the one just made", len(listed))
	}
	if got := member.status(http.MethodGet, "/api/instances/"+created.ID, nil); got != http.StatusOK {
		t.Errorf("the creator cannot reach the server they just made: %d", got)
	}
}

// Deleting one drops it from the grants that named it, so a list of servers
// that no longer exist does not build up where permissions are read.
func TestDeletingAnInstanceClearsItFromGrants(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	doomed := env.createInstance("doomed")
	kept := env.createInstance("kept")
	env.grantOnly("ops", users.RoleOps, doomed.ID, kept.ID)

	if got := env.status(http.MethodDelete, "/api/instances/"+doomed.ID, nil); got != http.StatusNoContent {
		t.Fatalf("deleting the instance: %d", got)
	}
	account, _ := env.accounts.ByUsername("ops")
	if len(account.Instances) != 1 || account.Instances[0] != kept.ID {
		t.Errorf("the grant is now %v, want only the surviving server", account.Instances)
	}
}

// The field-level half of CapInstanceLaunch: one route carries both "rename
// this" and "run this instead", so the capability follows the fields.
func TestLaunchFieldsNeedTheLaunchCapability(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	created := env.createInstance("server")

	// 运维 may not change what runs.
	ops := env.grantOnly("ops", users.RoleOps, created.ID)
	if _, err := env.accounts.UpdateRole(users.RoleOps, "运维", []authz.Cap{
		authz.CapInstanceView, authz.CapInstanceSettings,
	}, nil); err != nil {
		t.Fatalf("UpdateRole: %v", err)
	}

	// A harmless edit goes through.
	if got := ops.status(http.MethodPut, "/api/instances/"+created.ID,
		map[string]any{"name": "renamed"}); got != http.StatusOK {
		t.Errorf("renaming with CapInstanceSettings: %d, want 200", got)
	}
	// Every dangerous field is refused, one at a time.
	for _, field := range launchFields {
		body := map[string]any{"name": "renamed", field: "x"}
		if field == "jvmArgs" || field == "serverArgs" || field == "argFiles" || field == "command" {
			body[field] = []string{"x"}
		}
		if got := ops.status(http.MethodPut, "/api/instances/"+created.ID, body); got != http.StatusForbidden {
			t.Errorf("PUT carrying %q without CapInstanceLaunch: %d, want 403", field, got)
		}
	}

	// Even echoed back unchanged: the server cannot tell "unchanged" from
	// "changed" without taking the client's word for it.
	if got := ops.status(http.MethodPut, "/api/instances/"+created.ID,
		map[string]any{"name": "renamed", "jar": ""}); got != http.StatusForbidden {
		t.Error("an unchanged launch field was allowed through without the capability")
	}

	// The administrator, who holds it, is unaffected.
	if got := env.status(http.MethodPut, "/api/instances/"+created.ID,
		map[string]any{"name": "renamed", "command": []string{"/bin/sh"}}); got != http.StatusOK {
		t.Errorf("the administrator was refused a launch edit: %d", got)
	}
}

// Request types that name a server in their body cannot be caught by
// requireInstance, which only sees the path. This pins the set so a new one has
// to be classified rather than quietly shipping unscoped.
func TestBodyScopedRequestTypesArePinned(t *testing.T) {
	// Types carrying an "instanceId" field, and what each one is.
	//
	//   installSchematicRequest — a request. Its handler calls mayUseInstance.
	//   the rest              — responses, built from visibleInstances.
	//
	// A type appearing here that is not in this list is a decision somebody has
	// to make: if it is a request, its handler must check the grant itself.
	want := map[string]bool{
		"installSchematicRequest": true,
		"overviewUse":             true,
		"overviewForeign":         true,
		"velocityCandidate":       true,
		"networkProxyEntry":       true,
	}

	for _, name := range typesWithJSONField(t, "instanceId") {
		if !want[name] {
			t.Errorf("%s carries an instanceId and is not classified. If it is a request "+
				"type, its handler has to call mayUseInstance — requireInstance only sees "+
				"the path. If it is a response, build it from visibleInstances.", name)
		}
		delete(want, name)
	}
	for name := range want {
		t.Errorf("%s is classified but no longer carries an instanceId field", name)
	}
}

// typesWithJSONField returns the names of every struct in the package with a
// field carrying the given json tag. Read from the source rather than by
// reflection because the types are unexported and scattered, and because what
// this test is about is what somebody wrote, not what happens to be reachable.
func typesWithJSONField(t *testing.T, tag string) []string {
	t.Helper()

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package directory: %v", err)
	}

	var found []string
	fset := token.NewFileSet()
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			spec, ok := n.(*ast.TypeSpec)
			if !ok {
				return true
			}
			st, ok := spec.Type.(*ast.StructType)
			if !ok {
				return true
			}
			for _, field := range st.Fields.List {
				if field.Tag == nil {
					continue
				}
				value, err := strconv.Unquote(field.Tag.Value)
				if err != nil {
					continue
				}
				name, _, _ := strings.Cut(reflect.StructTag(value).Get("json"), ",")
				if name == tag {
					found = append(found, spec.Name.Name)
					return true
				}
			}
			return true
		})
	}
	slices.Sort(found)
	return slices.Compact(found)
}

// readUntilError drains the console socket until the panel says something went
// wrong, and returns what it said.
func readUntilError(t *testing.T, conn *websocket.Conn) string {
	t.Helper()

	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	for range 10 {
		_, data, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("read frame: %v", err)
		}
		var msg struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		}
		if err := json.Unmarshal(data, &msg); err != nil {
			// Binary console output in TTY mode; not a notice.
			continue
		}
		if msg.Type == "error" {
			return msg.Message
		}
	}
	t.Fatal("no error frame arrived")
	return ""
}

// The console socket carries two capabilities in two directions. Watching is
// CapInstanceView, which the route declares; typing is CapInstanceConsole,
// which has to be checked per message — and a caller without it gets a
// read-only socket rather than a refused handshake.
func TestConsoleSocketIsReadOnlyWithoutTheConsoleCapability(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	created := env.createInstance("server")

	watcher, err := env.accounts.AddRole("只能看", []authz.Cap{authz.CapInstanceView}, nil)
	if err != nil {
		t.Fatalf("AddRole: %v", err)
	}
	member := env.grantOnly("watcher", watcher.ID, created.ID)

	// The handshake succeeds: this account may watch.
	conn := member.dialConsole(created.ID)
	if err := conn.WriteJSON(map[string]string{"type": "command", "command": "list"}); err != nil {
		t.Fatalf("send command: %v", err)
	}
	if got := readUntilError(t, conn); !strings.Contains(got, "发送控制台命令") {
		t.Errorf("the socket answered %q, want a refusal naming the capability", got)
	}

	// 运维 holds it, and gets the ordinary answer instead — the server is not
	// running, which is a different refusal and proves the command got through.
	ops := env.grantOnly("ops", users.RoleOps, created.ID)
	opsConn := ops.dialConsole(created.ID)
	if err := opsConn.WriteJSON(map[string]string{"type": "command", "command": "list"}); err != nil {
		t.Fatalf("send command: %v", err)
	}
	if got := readUntilError(t, opsConn); !strings.Contains(got, "未在运行") {
		t.Errorf("运维 got %q, want the not-running answer", got)
	}
}

// Installing a build names its server in the body, so the grant is checked
// inside the handler — before anything is looked up, so an ungranted server
// answers exactly as an id that does not exist.
func TestSchematicInstallChecksTheGrant(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	mine := env.createInstance("mine")
	theirs := env.createInstance("theirs")

	role, err := env.accounts.AddRole("建筑", []authz.Cap{
		authz.CapInstanceView, authz.CapInstanceSchematics, authz.CapLibrarySchems,
	}, nil)
	if err != nil {
		t.Fatalf("AddRole: %v", err)
	}
	member := env.grantOnly("builder", role.ID, mine.ID)

	// The schematic id does not exist either way. The ungranted server has to
	// be refused first, or the refusal itself would confirm the server is real.
	if got := member.status(http.MethodPost, "/api/schematics/nope/install",
		installSchematicRequest{InstanceID: theirs.ID}); got != http.StatusNotFound {
		t.Errorf("installing into an ungranted server: %d, want 404", got)
	}
	// Into the granted one it gets as far as the missing schematic, which is a
	// different failure — the grant check let it through.
	resp := member.do(http.MethodPost, "/api/schematics/nope/install",
		installSchematicRequest{InstanceID: mine.ID})
	body := readAll(t, resp)
	if strings.Contains(body, "实例不存在") {
		t.Errorf("installing into a granted server was refused by the grant check: %s", body)
	}
}

// The directory rule, through the routes an operator actually uses.
//
// The unit tests in internal/serverfiles prove the confinement; this proves it
// is wired to the file manager and to the role the caller holds.
func TestRolePathsConfineTheFileManager(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	created := env.createInstance("server")

	// Two files, made as the administrator: one inside the rule, one outside.
	// WriteText does not create parents, so the folder comes first.
	if got := env.status(http.MethodPost, "/api/instances/"+created.ID+"/files/mkdir",
		map[string]string{"path": "plugins/MyPlugin"}); got != http.StatusNoContent && got != http.StatusOK {
		t.Fatalf("creating the plugin folder: %d", got)
	}
	for _, spec := range []struct{ path, content string }{
		{"plugins/MyPlugin/config.yml", "mine: true\n"},
		{"server.properties", "motd=hello\n"},
	} {
		if got := env.status(http.MethodPut, "/api/instances/"+created.ID+"/files/content",
			map[string]string{"path": spec.path, "content": spec.content}); got != http.StatusNoContent &&
			got != http.StatusOK {
			t.Fatalf("seeding %s: %d", spec.path, got)
		}
	}

	role, err := env.accounts.AddRole("插件作者", []authz.Cap{
		authz.CapInstanceView, authz.CapInstanceFilesRead, authz.CapInstanceFilesWrite,
	}, []string{"plugins/MyPlugin"})
	if err != nil {
		t.Fatalf("AddRole: %v", err)
	}
	member := env.grantOnly("author", role.ID, created.ID)
	base := "/api/instances/" + created.ID + "/files"

	// Inside the rule everything works.
	if got := member.status(http.MethodGet, base+"/content?path=plugins/MyPlugin/config.yml", nil); got != http.StatusOK {
		t.Errorf("reading inside the rule: %d, want 200", got)
	}
	if got := member.status(http.MethodPut, base+"/content",
		map[string]string{"path": "plugins/MyPlugin/config.yml", "content": "mine: 2\n"}); got != http.StatusNoContent {
		t.Errorf("writing inside the rule: %d, want 204", got)
	}

	// Outside it, nothing does — and it reports as missing rather than
	// refused, so the refusal does not confirm what is there.
	if got := member.status(http.MethodGet, base+"/content?path=server.properties", nil); got != http.StatusNotFound {
		t.Errorf("reading outside the rule: %d, want 404", got)
	}
	if got := member.status(http.MethodPut, base+"/content",
		map[string]string{"path": "server.properties", "content": "motd=pwned\n"}); got != http.StatusNotFound {
		t.Errorf("writing outside the rule: %d, want 404", got)
	}
	if got := member.status(http.MethodDelete, base+"?path=server.properties", nil); got != http.StatusNotFound {
		t.Errorf("deleting outside the rule: %d, want 404", got)
	}

	// The root listing shows the way down and nothing else, or the file
	// manager would have no way to reach the one folder it may open.
	var listing struct {
		Entries []struct {
			Name string `json:"name"`
		} `json:"entries"`
	}
	decodeBody(t, member.do(http.MethodGet, base+"?path=/", nil), &listing)
	if len(listing.Entries) != 1 || listing.Entries[0].Name != "plugins" {
		t.Errorf("root listing is %+v, want only the way down to the rule", listing.Entries)
	}

	// The administrator is unaffected: the rule is the role's, not the file's.
	if got := env.status(http.MethodGet, base+"/content?path=server.properties", nil); got != http.StatusOK {
		t.Errorf("the administrator was confined too: %d", got)
	}
}

// A role with no rule reaches the whole instance directory, which is what every
// role did before this existed.
func TestRoleWithoutPathsIsNotConfined(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	created := env.createInstance("server")
	if got := env.status(http.MethodPut, "/api/instances/"+created.ID+"/files/content",
		map[string]string{"path": "server.properties", "content": "motd=hello\n"}); got != http.StatusNoContent {
		t.Fatalf("seeding: %d", got)
	}

	role, err := env.accounts.AddRole("全目录", []authz.Cap{
		authz.CapInstanceView, authz.CapInstanceFilesRead,
	}, nil)
	if err != nil {
		t.Fatalf("AddRole: %v", err)
	}
	member := env.grantOnly("everywhere", role.ID, created.ID)

	if got := member.status(http.MethodGet,
		"/api/instances/"+created.ID+"/files/content?path=server.properties", nil); got != http.StatusOK {
		t.Errorf("a role with no directory rule was confined anyway: %d", got)
	}
}
