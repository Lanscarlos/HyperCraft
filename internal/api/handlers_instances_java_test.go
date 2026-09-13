package api

import (
	"net/http"
	"testing"

	"github.com/lanscarlos/hypercraft/internal/instance"
	"github.com/lanscarlos/hypercraft/internal/javaruntime"
)

func TestUpdateInstanceRejectsAnUnregisteredJava(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	created := env.createInstance("srv")

	resp := env.do(http.MethodPut, "/api/instances/"+created.ID, instanceRequest{
		Name: created.Name, Directory: created.Directory, Java: "/opt/sneaky/bin/java",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400 for an unregistered java, got %d", resp.StatusCode)
	}
}

func TestUpdateInstanceAcceptsARegisteredJava(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	launcher := fakeJavaLauncher(t)

	registered := env.do(http.MethodPost, "/api/java/registry", registerJavaRequest{Path: launcher})
	registered.Body.Close()
	if registered.StatusCode != http.StatusCreated {
		t.Fatalf("register: %d", registered.StatusCode)
	}

	created := env.createInstance("srv")
	resp := env.do(http.MethodPut, "/api/instances/"+created.ID, instanceRequest{
		Name: created.Name, Directory: created.Directory, Java: launcher,
	})
	var updated instance.Status
	decodeBody(t, resp, &updated)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	if updated.Java != launcher {
		t.Fatalf("java = %q, want the registered launcher", updated.Java)
	}
}

// The whole point of checking the change rather than the state: an instance
// whose java predates the registry must stay editable, or a Java problem would
// block renaming the server.
func TestUpdateInstanceAllowsAnUnchangedLegacyJava(t *testing.T) {
	env := newTestEnv(t)
	env.login()

	// Registered so the instance can be pointed at it, then dropped, which is
	// exactly the state a deleted runtime or a failed migration leaves behind.
	legacy := env.pointInstanceAt("srv", "/opt/legacy/bin/java")
	if err := env.api.java.Registry().Remove(javaruntime.EntryID("/opt/legacy/bin/java")); err != nil {
		t.Fatalf("drop the entry: %v", err)
	}

	resp := env.do(http.MethodPut, "/api/instances/"+legacy.ID, instanceRequest{
		Name: "renamed", Directory: legacy.Directory, Java: "/opt/legacy/bin/java",
	})
	var updated instance.Status
	decodeBody(t, resp, &updated)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("an unchanged java must not block the save, got %d", resp.StatusCode)
	}
	if updated.Name != "renamed" {
		t.Fatalf("the rename should have landed, name = %q", updated.Name)
	}
}

func TestUpdateInstanceIgnoresAnOmittedJava(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	legacy := env.pointInstanceAt("srv", "/opt/legacy/bin/java")
	if err := env.api.java.Registry().Remove(javaruntime.EntryID("/opt/legacy/bin/java")); err != nil {
		t.Fatalf("drop the entry: %v", err)
	}

	resp := env.do(http.MethodPut, "/api/instances/"+legacy.ID, instanceRequest{
		Name: "renamed", Directory: legacy.Directory,
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
}

func TestCreateInstanceRejectsAnUnregisteredJava(t *testing.T) {
	env := newTestEnv(t)
	env.login()

	resp := env.do(http.MethodPost, "/api/instances", instanceRequest{
		Name: "new", Directory: t.TempDir(), Java: "/opt/sneaky/bin/java",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", resp.StatusCode)
	}
}

// Blank means "I did not choose", which applyDefaults turns into "java". That
// is the historical behaviour and breaking it buys nothing.
func TestCreateInstanceAllowsABlankJava(t *testing.T) {
	env := newTestEnv(t)
	env.login()

	resp := env.do(http.MethodPost, "/api/instances", instanceRequest{
		Name: "new", Directory: t.TempDir(),
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("want 201, got %d", resp.StatusCode)
	}
}
