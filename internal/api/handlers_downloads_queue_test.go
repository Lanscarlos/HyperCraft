package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/lanscarlos/hypercraft/internal/authz"
	"github.com/lanscarlos/hypercraft/internal/download"
)

// seedOneJobPerKind puts one job of every shelf on the queue and returns their
// ids. They never run: with no Attempts they fail on the worker, which is fine
// — what is under test is who may see them, and a failed job is still a row.
func seedOneJobPerKind(t *testing.T, env *testEnv) map[download.Kind]string {
	t.Helper()
	ids := make(map[download.Kind]string, 4)
	for _, kind := range []download.Kind{
		download.KindCore, download.KindJava, download.KindDatabase, download.KindPlugin,
	} {
		job, err := env.downloads.Submit(download.Request{
			Kind:      kind,
			Title:     string(kind) + " thing",
			DedupeKey: string(kind),
		})
		if err != nil {
			t.Fatalf("submit %s: %v", kind, err)
		}
		ids[kind] = job.ID
	}
	return ids
}

func kindsIn(t *testing.T, env *testEnv) []download.Kind {
	t.Helper()
	var body downloadsResponse
	resp := env.do(http.MethodGet, "/api/downloads", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/downloads: %d", resp.StatusCode)
	}
	decodeBody(t, resp, &body)
	out := make([]download.Kind, 0, len(body.Jobs))
	for _, job := range body.Jobs {
		out = append(out, job.Kind)
	}
	return out
}

// roleWith builds an account holding exactly these capabilities.
func roleWith(t *testing.T, env *testEnv, name string, caps ...authz.Cap) *testEnv {
	t.Helper()
	role, err := env.accounts.AddRole(name, caps, nil)
	if err != nil {
		t.Fatalf("AddRole(%s): %v", name, err)
	}
	// Usernames take no spaces; the role's name is prose and may.
	return env.asMember(strings.ReplaceAll(name, " ", "-"), role.ID)
}

func hasKind(kinds []download.Kind, want download.Kind) bool {
	for _, kind := range kinds {
		if kind == want {
			return true
		}
	}
	return false
}

// Merging four endpoints into one must not merge four capabilities into one.
//
// Every shelf's downloads used to hang off that shelf's own route, which the
// middleware gated. The panel-wide list has no single capability that could
// stand for all four — an account that may see plugin jars has no business
// learning which JDKs the panel is installing — so the filter moved into the
// handler, and this is what holds it there.
func TestTheDownloadsListIsFilteredPerKind(t *testing.T) {
	cases := []struct {
		name string
		caps []authz.Cap
		want []download.Kind
		deny []download.Kind
	}{
		{
			name: "only plugins",
			caps: []authz.Cap{authz.CapLibraryPlugins},
			want: []download.Kind{download.KindPlugin},
			deny: []download.Kind{download.KindCore, download.KindJava, download.KindDatabase},
		},
		{
			name: "only java",
			caps: []authz.Cap{authz.CapPanelJava},
			want: []download.Kind{download.KindJava},
			deny: []download.Kind{download.KindCore, download.KindPlugin, download.KindDatabase},
		},
		{
			name: "cores and databases",
			caps: []authz.Cap{authz.CapLibraryCores, authz.CapPanelDatabases},
			want: []download.Kind{download.KindCore, download.KindDatabase},
			deny: []download.Kind{download.KindJava, download.KindPlugin},
		},
		{
			name: "nothing at all",
			caps: nil,
			deny: []download.Kind{
				download.KindCore, download.KindJava,
				download.KindDatabase, download.KindPlugin,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newTestEnv(t)
			env.login()
			seedOneJobPerKind(t, env)

			member := roleWith(t, env, tc.name, tc.caps...)
			got := kindsIn(t, member)

			for _, kind := range tc.want {
				if !hasKind(got, kind) {
					t.Errorf("%s is missing, and this account may see it", kind)
				}
			}
			for _, kind := range tc.deny {
				if hasKind(got, kind) {
					t.Errorf("%s leaked to an account without the capability for it", kind)
				}
			}
		})
	}
}

// An administrator holds everything, so the filter must not be subtracting from
// them either — a test that only proves things are hidden would pass against a
// handler that returns nothing at all.
func TestAnAdministratorSeesEveryKind(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	seedOneJobPerKind(t, env)

	got := kindsIn(t, env)
	for _, kind := range []download.Kind{
		download.KindCore, download.KindJava, download.KindDatabase, download.KindPlugin,
	} {
		if !hasKind(got, kind) {
			t.Errorf("%s is missing from the administrator's list", kind)
		}
	}
}

// The top bar's badge counts what this endpoint returns, so it is counted here
// rather than in the browser: a number that disagrees with the list is a number
// that tells an account something it may not see.
func TestTheActiveCountMatchesTheFilteredList(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	seedOneJobPerKind(t, env)

	member := roleWith(t, env, "plugins only", authz.CapLibraryPlugins)
	var body downloadsResponse
	resp := member.do(http.MethodGet, "/api/downloads", nil)
	defer resp.Body.Close()
	decodeBody(t, resp, &body)

	if body.Active > len(body.Jobs) {
		t.Fatalf("active = %d but the list holds %d rows", body.Active, len(body.Jobs))
	}
	for _, job := range body.Jobs {
		if job.Kind != download.KindPlugin {
			t.Fatalf("a %s job reached an account with only the plugin capability", job.Kind)
		}
	}
}

// Cancelling by id is the one place an account could reach a job it cannot see,
// because the id is all the request carries.
//
// 404 rather than 403: whether that id exists is itself something this account
// is not entitled to learn.
func TestCancellingAJobOfAnUnseenKindIsNotFound(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	ids := seedOneJobPerKind(t, env)

	member := roleWith(t, env, "plugins only", authz.CapLibraryPlugins)
	got := member.status(http.MethodPost, "/api/downloads/"+ids[download.KindJava]+"/cancel", nil)
	if got != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 — a job you cannot see must not be distinguishable from one that does not exist", got)
	}
}

// And the same account may of course cancel its own shelf's.
func TestCancellingAJobYouCanSeeWorks(t *testing.T) {
	env := newTestEnv(t)
	env.login()
	ids := seedOneJobPerKind(t, env)

	member := roleWith(t, env, "plugins only", authz.CapLibraryPlugins)
	got := member.status(http.MethodPost, "/api/downloads/"+ids[download.KindPlugin]+"/cancel", nil)
	if got != http.StatusOK && got != http.StatusConflict {
		// Conflict is the honest answer for a job that already failed for want
		// of an Attempts function; what must not happen is 404 or 403.
		t.Fatalf("status = %d, want the cancel to be reachable", got)
	}
}

// Signing in is the only gate on the list route itself. Hanging any one
// capability on it in the router would keep the other three shelves' jobs from
// an account that holds them — which is the whole reason the filter is in the
// handler.
func TestTheDownloadsRouteIsNotGatedByAnyOneCapability(t *testing.T) {
	env := newTestEnv(t)
	env.login()

	member := roleWith(t, env, "empty role")
	if got := member.status(http.MethodGet, "/api/downloads", nil); got != http.StatusOK {
		t.Fatalf("status = %d, want 200 with an empty list rather than a refusal", got)
	}
}
