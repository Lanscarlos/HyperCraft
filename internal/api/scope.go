package api

import (
	"net/http"
	"strings"

	"github.com/lanscarlos/hypercraft/internal/instance"
)

// Which servers a request may touch.
//
// This is the second half of the permission model, and it is orthogonal to the
// first: capabilities say what may be done, a grant says to which servers. A
// request needs both. See docs/proposal-multi-user.md.
//
// The awkward part is not the check, it is remembering to make it. Most of the
// panel's cross-instance views existed long before accounts did and call
// mgr.List() to build a picture of the whole machine, so the leak is not in the
// routes that name an instance — those are handled once, below — but in the
// aggregates: "which servers use this Java runtime", "which servers have this
// plugin out of date", "which ports are taken".
//
// So mgr.List() is not called from a handler any more. Every caller goes
// through visibleInstances or allInstances, which forces the question to be
// answered out loud, and TestHandlersDoNotListInstancesDirectly keeps it that
// way.

// visibleInstances is every server the caller may touch, for the lists and
// aggregates a caller is shown.
func (s *Server) visibleInstances(r *http.Request) []*instance.Instance {
	who, ok := principalFrom(r.Context())
	all := s.mgr.List()
	if !ok {
		// Unreachable behind requireAuth. Returning nothing is the safe
		// direction for a bug that should not exist.
		return nil
	}
	if who.user.Instances == nil {
		return all
	}

	out := make([]*instance.Instance, 0, len(all))
	for _, inst := range all {
		if s.accounts.CanUseInstance(who.user, inst.Config().ID) {
			out = append(out, inst)
		}
	}
	return out
}

// allInstances is every server on the machine, whoever is asking.
//
// It is right in exactly one situation: a check whose answer would be wrong if
// it ignored a server the caller cannot see. Deleting a Java runtime still used
// by somebody else's server breaks that server; a port already taken is taken
// whoever took it; a directory already belonging to an instance must not be
// handed to a second one. Hiding those would not protect anything — it would
// produce a panel that lets one person quietly break another person's server.
//
// The cost is that such a check can name a server the caller cannot otherwise
// see. That is the right trade for a refusal that has to explain itself, and it
// is why every use of this is behind a panel-wide capability.
func (s *Server) allInstances() []*instance.Instance { return s.mgr.List() }

// mayUseInstance reports whether the caller's grant covers one server.
func (s *Server) mayUseInstance(r *http.Request, instanceID string) bool {
	who, ok := principalFrom(r.Context())
	return ok && s.accounts.CanUseInstance(who.user, instanceID)
}

// refuseInstance is how a server outside the caller's grant is reported.
//
// 404 rather than 403, and the same message the manager gives for an id that
// does not exist: 403 would confirm that this id is a real server, which is
// precisely what a grant is meant not to tell people.
func refuseInstance(w http.ResponseWriter) {
	writeError(w, http.StatusNotFound, "实例不存在")
}

// instanceScopedPattern reports whether a route names its instance in the path,
// which is what lets requireInstance handle it without the handler's help.
//
// Derived from the pattern rather than declared in the table on purpose: every
// route under /api/instances/{id} is about that instance by construction, and a
// flag that had to be remembered would eventually not be. Routes that name an
// instance somewhere else — in a body, or by fanning out across the fleet —
// cannot be caught this way and are checked inside their handlers; see
// TestBodyScopedRoutesCheckTheGrant for the list.
func instanceScopedPattern(pattern string) bool {
	_, path, ok := strings.Cut(pattern, " ")
	if !ok {
		return false
	}
	const prefix = "/api/instances/{id}"
	return path == prefix || strings.HasPrefix(path, prefix+"/")
}

// requireInstance refuses a route whose {id} is outside the caller's grant. It
// runs before the capability check, so a server the caller may not see reports
// the same 404 whatever capabilities they hold.
func (s *Server) requireInstance(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.mayUseInstance(r, r.PathValue("id")) {
			refuseInstance(w)
			return
		}
		next(w, r)
	}
}
