package api

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/lanscarlos/hypercraft/internal/config"
	"github.com/lanscarlos/hypercraft/internal/instance"
	"github.com/lanscarlos/hypercraft/internal/plugin"
)

// writePluginError maps the plugin package's sentinels onto HTTP statuses.
func (s *Server) writePluginError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, plugin.ErrInvalidRepo), errors.Is(err, plugin.ErrInvalidTarget),
		errors.Is(err, plugin.ErrInvalidID):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, plugin.ErrNeedsToken):
		// Not a 404: the repository may well exist, and repeating the request
		// unchanged will never work. What is missing is a credential the
		// operator has to supply, so this is their request to fix.
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, plugin.ErrNotFound), errors.Is(err, plugin.ErrNotInstalled):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, plugin.ErrExists), errors.Is(err, plugin.ErrBusy):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, plugin.ErrNotAJar):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, plugin.ErrTooLarge):
		writeError(w, http.StatusRequestEntityTooLarge, err.Error())
	case errors.Is(err, plugin.ErrRateLimited):
		// Not the panel's fault and not the operator's either; 429 is the one
		// status that says "the same request will work later".
		writeError(w, http.StatusTooManyRequests, err.Error())
	case errors.Is(err, plugin.ErrUpstream), errors.Is(err, plugin.ErrNoRelease),
		errors.Is(err, plugin.ErrNoAsset):
		writeError(w, http.StatusBadGateway, err.Error())
	default:
		s.writeFileError(w, err)
	}
}

// pluginsAvailable reports the "not wired up" case in one place, matching how
// core downloads and Java installs behave when the panel is built without them.
func (s *Server) pluginsAvailable(w http.ResponseWriter) bool {
	if s.plugins == nil || s.instancePlugins == nil {
		writeError(w, http.StatusNotFound, "plugin management is not enabled on this panel")
		return false
	}
	return true
}

// pluginView is a tracked plugin plus the instances running it, so the library
// page can say what a plugin is actually being used for before it is deleted.
type pluginView struct {
	plugin.Plugin
	UsedBy []string `json:"usedBy"`
}

type pluginLibraryResponse struct {
	Root    string       `json:"root"`
	Plugins []pluginView `json:"plugins"`
	Job     *plugin.Job  `json:"job"`
	// Tokens are the GitHub credentials the panel holds, default first. The
	// secrets never travel — an entry is a name, a four-character tail and what
	// the panel knows about that token's quota, which is everything the settings
	// page and the source pickers need and nothing that could be replayed.
	Tokens []pluginTokenView `json:"tokens"`
	// TokenConfigured says whether the panel holds any GitHub access token,
	// which is what private repositories and a higher API rate limit both need,
	// and TokenHint is the default one's tail. Both are what Tokens says, kept
	// for clients written when there could only be one.
	TokenConfigured bool   `json:"tokenConfigured"`
	TokenHint       string `json:"tokenHint,omitempty"`
	// Mirrors are the download proxies to choose between, and Mirror is the one
	// in use. Static per build, but they travel with the listing so the settings
	// page needs no second request to render the picker.
	Mirrors []plugin.Mirror `json:"mirrors"`
	Mirror  string          `json:"mirror"`
	// Budget is the GitHub API quota as of the last call the panel made. It
	// rides along with the listing rather than being asked for, because asking
	// would spend the thing it reports on — and because the page that shows it
	// is the page whose buttons spend it.
	Budget plugin.Budget `json:"budget"`
}

// handlePluginLibrary answers everything the library page needs in one request.
func (s *Server) handlePluginLibrary(w http.ResponseWriter, _ *http.Request) {
	if s.plugins == nil || s.instancePlugins == nil {
		writeJSON(w, http.StatusOK, pluginLibraryResponse{Plugins: []pluginView{}})
		return
	}
	writeJSON(w, http.StatusOK, s.pluginLibrary())
}

func (s *Server) pluginLibrary() pluginLibraryResponse {
	names := make(map[string]string)
	for _, inst := range s.mgr.List() {
		cfg := inst.Config()
		names[cfg.ID] = cfg.Name
	}
	users := s.instancePlugins.UsedBy()

	items := s.plugins.Library().List()
	tokens := s.tokenViews(items)
	resp := pluginLibraryResponse{
		Root:            s.plugins.Library().Root(),
		Plugins:         make([]pluginView, 0, len(items)),
		Tokens:          tokens,
		TokenConfigured: len(tokens) > 0,
		Mirrors:         plugin.Mirrors(),
		Mirror:          s.plugins.Client().Mirror(),
		Budget:          s.plugins.Client().Budget(),
	}
	if len(tokens) > 0 {
		resp.TokenHint = tokens[0].Hint
	}
	for _, item := range items {
		view := pluginView{Plugin: item, UsedBy: []string{}}
		for _, instanceID := range users[item.ID] {
			// An instance deleted while the panel was down leaves a record
			// behind; name it only if it still exists.
			if name, ok := names[instanceID]; ok {
				view.UsedBy = append(view.UsedBy, name)
			}
		}
		resp.Plugins = append(resp.Plugins, view)
	}
	if job, ok := s.plugins.Status(); ok {
		resp.Job = &job
	}
	return resp
}

// pluginTokenView is one stored credential as the panel is willing to describe
// it: what it is called, which four characters it ends in, how many plugins
// point at it, and what GitHub last said about its quota. The secret is not
// here and has no route out of the panel.
type pluginTokenView struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Hint string `json:"hint,omitempty"`
	// Default marks the token every source that names none is read with. It is
	// the head of the list rather than a flag on the config, so there is no
	// second copy of the answer to keep true.
	Default bool `json:"default,omitempty"`
	// UsedBy counts the tracked plugins that name this token, which is what
	// makes deleting one a decision rather than a click.
	UsedBy int `json:"usedBy"`
	// Budget is this token's own remaining API quota. Per token because the
	// quotas are: three credentials are three separate 5000-an-hour ceilings.
	Budget plugin.Budget `json:"budget"`
}

// PluginTokens converts stored credentials into what the plugin client reads
// sources with. Exported because the daemon does the same wiring at boot.
func PluginTokens(tokens []config.GitHubToken) []plugin.Token {
	out := make([]plugin.Token, 0, len(tokens))
	for _, token := range tokens {
		out = append(out, plugin.Token{ID: token.ID, Name: token.Name, Secret: token.Token})
	}
	return out
}

// githubTokens is the stored credential list, read under the panel lock like
// every other piece of panel config. The copy matters: callers edit the slice
// they get back and hand it to applyGitHubTokens.
func (s *Server) githubTokens() []config.GitHubToken {
	s.panelMu.RLock()
	defer s.panelMu.RUnlock()
	return append([]config.GitHubToken(nil), s.panel.GitHubTokens...)
}

// tokenViews describes the stored tokens for the library page, counting how
// many of the tracked plugins each one is answerable for.
func (s *Server) tokenViews(items []plugin.Plugin) []pluginTokenView {
	stored := s.githubTokens()
	used := make(map[string]int, len(stored))
	for _, item := range items {
		if item.Source.Kind == plugin.SourceGitHub && item.Source.TokenID != "" {
			used[item.Source.TokenID]++
		}
	}

	out := make([]pluginTokenView, 0, len(stored))
	for i, token := range stored {
		view := pluginTokenView{
			ID:      token.ID,
			Name:    token.Name,
			Hint:    tokenHint(token.Token),
			Default: i == 0,
			UsedBy:  used[token.ID],
			Budget:  s.plugins.Client().BudgetOf(token.ID),
		}
		if view.Default {
			// A default token also answers for everything that names none,
			// which is most of a library upgraded from the single-token panel.
			for _, item := range items {
				if item.Source.Kind == plugin.SourceGitHub && item.Source.TokenID == "" {
					view.UsedBy++
				}
			}
		}
		out = append(out, view)
	}
	return out
}

// tokenHint is the tail of a token, enough to recognise one already stored and
// not enough to be worth stealing. Short strings get nothing: a "hint" that
// shows half a secret is not a hint.
func tokenHint(token string) string {
	if len(token) < 12 {
		return ""
	}
	return token[len(token)-4:]
}

// applyGitHubTokens puts a new credential list into effect and saves it.
//
// The client goes first, so a token pasted to fix a failing repository works on
// the retry that follows even if writing panel.json goes wrong. It reports
// whether the response has already been written.
func (s *Server) applyGitHubTokens(w http.ResponseWriter, tokens []config.GitHubToken) bool {
	s.plugins.Client().SetTokens(PluginTokens(tokens))

	s.panelMu.Lock()
	panel := s.panel
	panel.GitHubTokens = tokens
	// A panel upgraded mid-session may still be carrying the pre-list field.
	// Leaving it would resurrect the old token on the next boot, on top of
	// whatever the operator has just done here.
	panel.GitHubToken = ""
	s.panel = panel
	s.panelMu.Unlock()

	if err := s.persistPanel(); err != nil {
		s.log.Error("could not persist the GitHub tokens", "err", err)
		writeError(w, http.StatusInternalServerError, "令牌已生效，但保存失败，重启面板后会丢失")
		return false
	}
	return true
}

type pluginTokenRequest struct {
	// Name is the operator's label for this credential — which account or
	// organisation it speaks for. It is what error messages call the token.
	Name string `json:"name"`
	// Token is a GitHub personal access token. Empty when renaming an existing
	// entry, and — on the single-token route — a request to forget the default.
	Token string `json:"token"`
	// Default asks for this token to become the one sources that name none are
	// read with. Only meaningful on an update.
	Default bool `json:"default"`
}

// handlePluginTokens adds a credential to the panel's list.
//
// Tokens are write-only across this API: they go in here and are never sent
// back, so a panel session that is later hijacked cannot lift the operator's
// GitHub account out of it. Replacing a wrong one is a write over the top of it.
func (s *Server) handlePluginTokens(w http.ResponseWriter, r *http.Request) {
	if !s.pluginsAvailable(w) {
		return
	}

	var req pluginTokenRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}
	token := strings.TrimSpace(req.Token)
	if token == "" {
		writeError(w, http.StatusBadRequest, "请粘贴一个访问令牌")
		return
	}
	if err := validateGitHubToken(token); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	name, err := tokenName(req.Name)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	tokens := s.githubTokens()
	if name == "" {
		name = fmt.Sprintf("令牌 %d", len(tokens)+1)
	}
	entry := config.GitHubToken{ID: newTokenID(tokens), Name: name, Token: token}
	if !s.applyGitHubTokens(w, append(tokens, entry)) {
		return
	}
	s.log.Info("GitHub token added", "token", entry.ID, "name", entry.Name)
	writeJSON(w, http.StatusCreated, s.pluginLibrary())
}

// handleUpdatePluginToken renames a credential, replaces its secret, or makes
// it the default — the three things that can be done to a token that is already
// stored without detaching the plugins pointing at it, which is why they all
// keep the id.
func (s *Server) handleUpdatePluginToken(w http.ResponseWriter, r *http.Request) {
	if !s.pluginsAvailable(w) {
		return
	}

	var req pluginTokenRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}
	secret := strings.TrimSpace(req.Token)
	if err := validateGitHubToken(secret); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	name, err := tokenName(req.Name)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	id := r.PathValue("tokenId")
	tokens := s.githubTokens()
	at := indexOfToken(tokens, id)
	if at < 0 {
		writeError(w, http.StatusNotFound, "没有这个令牌")
		return
	}
	if name != "" {
		tokens[at].Name = name
	}
	if secret != "" {
		tokens[at].Token = secret
	}
	if req.Default && at != 0 {
		// The default is the head of the list, so promoting one is a move
		// rather than a flag — and the order of the rest is left alone.
		promoted := tokens[at]
		tokens = append(tokens[:at], tokens[at+1:]...)
		tokens = append([]config.GitHubToken{promoted}, tokens...)
	}

	if !s.applyGitHubTokens(w, tokens) {
		return
	}
	s.log.Info("GitHub token updated", "token", id, "rotated", secret != "", "default", req.Default)
	writeJSON(w, http.StatusOK, s.pluginLibrary())
}

// handleDeletePluginToken forgets a credential.
//
// The plugins that named it keep naming it, and start failing with "这个插件
// 指定的访问令牌已经不在了" until they are pointed at another. That is on
// purpose: quietly re-pointing them at whatever token is left would send one
// account's credential to a repository the operator had deliberately paired
// with another, and a 404 is a much worse way to find that out.
func (s *Server) handleDeletePluginToken(w http.ResponseWriter, r *http.Request) {
	if !s.pluginsAvailable(w) {
		return
	}

	id := r.PathValue("tokenId")
	tokens := s.githubTokens()
	at := indexOfToken(tokens, id)
	if at < 0 {
		writeError(w, http.StatusNotFound, "没有这个令牌")
		return
	}
	if !s.applyGitHubTokens(w, append(tokens[:at], tokens[at+1:]...)) {
		return
	}
	s.log.Info("GitHub token removed", "token", id)
	writeJSON(w, http.StatusOK, s.pluginLibrary())
}

// handlePluginToken is the single-token route the panel had before it could
// hold several, kept working for clients that still speak it: it writes the
// default token, and an empty body forgets it. Everything else about the list
// is left alone.
func (s *Server) handlePluginToken(w http.ResponseWriter, r *http.Request) {
	if !s.pluginsAvailable(w) {
		return
	}

	var req pluginTokenRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}
	token := strings.TrimSpace(req.Token)
	if err := validateGitHubToken(token); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	tokens := s.githubTokens()
	switch {
	case token == "" && len(tokens) > 0:
		tokens = tokens[1:]
	case token == "":
	case len(tokens) > 0:
		tokens[0].Token = token
	default:
		tokens = []config.GitHubToken{{ID: config.LegacyTokenID, Name: "默认令牌", Token: token}}
	}

	if !s.applyGitHubTokens(w, tokens) {
		return
	}
	if token == "" {
		s.log.Info("GitHub token cleared")
	} else {
		s.log.Info("GitHub token configured")
	}
	writeJSON(w, http.StatusOK, s.pluginLibrary())
}

// knownTokenID reports whether a source may name this credential. The empty id
// — "read it with the default" — is always allowed, including on a panel with
// no tokens at all, where it means anonymous.
func (s *Server) knownTokenID(id string) bool {
	id = strings.TrimSpace(id)
	return id == "" || indexOfToken(s.githubTokens(), id) >= 0
}

func indexOfToken(tokens []config.GitHubToken, id string) int {
	if id == "" {
		return -1
	}
	for i, token := range tokens {
		if token.ID == id {
			return i
		}
	}
	return -1
}

// newTokenID mints an id no stored token is using. Random rather than derived
// from the name, which the operator may reuse or change.
func newTokenID(tokens []config.GitHubToken) string {
	for {
		raw := make([]byte, 6)
		if _, err := rand.Read(raw); err != nil {
			// crypto/rand failing is unrecoverable, and a time-based id would
			// quietly collide instead of saying so.
			panic(fmt.Sprintf("hypercraft: crypto/rand unavailable: %v", err))
		}
		id := hex.EncodeToString(raw)
		if indexOfToken(tokens, id) < 0 {
			return id
		}
	}
}

// tokenName checks the label. It is shown on the settings page and inside error
// messages, so what is rejected is what would break either: control characters,
// and a length that is a paragraph rather than a name.
func tokenName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if len([]rune(name)) > 40 {
		return "", errors.New("令牌名字太长了，40 个字以内")
	}
	for _, r := range name {
		if r < ' ' || r == 0x7f {
			return "", errors.New("令牌名字里不能有控制字符")
		}
	}
	return name, nil
}

type pluginMirrorRequest struct {
	// Mirror is a mirror id from plugin.Mirrors(), a custom "https://…/"
	// prefix, or "" for the automatic order.
	Mirror string `json:"mirror"`
}

// handlePluginMirror chooses which proxy plugin jars are downloaded through.
//
// Deliberately not the same setting as the panel's own update mirror, even
// though both proxy the same CDN: plugins download weekly and the panel updates
// a few times a year, so the proxy that works for one is worth choosing without
// digging through a page about the other.
func (s *Server) handlePluginMirror(w http.ResponseWriter, r *http.Request) {
	if !s.pluginsAvailable(w) {
		return
	}

	var req pluginMirrorRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}
	mirror, err := plugin.ResolveMirror(req.Mirror)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	s.plugins.Client().SetMirror(mirror)

	s.panelMu.Lock()
	panel := s.panel
	panel.PluginMirror = mirror
	s.panel = panel
	s.panelMu.Unlock()

	if err := s.persistPanel(); err != nil {
		s.log.Error("could not persist the plugin download mirror", "err", err)
		writeError(w, http.StatusInternalServerError, "已生效，但保存失败，重启面板后会丢失")
		return
	}
	s.log.Info("plugin download mirror changed", "mirror", mirror)
	writeJSON(w, http.StatusOK, s.pluginLibrary())
}

// validateGitHubToken checks the shape of what was pasted, not whether GitHub
// accepts it — that answer only exists at the next request, and the plugin card
// is where it belongs. What is rejected here is what could not be a token at
// all: whitespace or control characters inside it (a pasted line that brought a
// newline along is trimmed before this), or a length no token has.
func validateGitHubToken(token string) error {
	if token == "" {
		return nil
	}
	if len(token) > 512 {
		return errors.New("这不像是一个访问令牌：太长了")
	}
	for _, r := range token {
		if r <= ' ' || r == 0x7f {
			return errors.New("访问令牌里不应该有空格或换行，请只粘贴令牌本身")
		}
	}
	return nil
}

type pluginRequest struct {
	Name         string `json:"name"`
	Repo         string `json:"repo"`
	AssetPattern string `json:"assetPattern"`
	Prerelease   bool   `json:"prerelease"`
	Private      bool   `json:"private"`
	// TokenID names which stored credential this repository is read with.
	// Empty is the default token, which is what a public repository wants.
	TokenID   string `json:"tokenId"`
	TargetDir string `json:"targetDir"`
	Note      string `json:"note"`
}

func (req pluginRequest) source() plugin.Source {
	return plugin.Source{
		Kind:         plugin.SourceGitHub,
		Repo:         strings.TrimSpace(req.Repo),
		AssetPattern: strings.TrimSpace(req.AssetPattern),
		Prerelease:   req.Prerelease,
		Private:      req.Private,
		TokenID:      strings.TrimSpace(req.TokenID),
	}
}

// handleAddPlugin starts tracking a plugin. It only registers the source —
// nothing is downloaded until the operator picks a version, so adding a plugin
// with a typo in the repository name costs a 404 rather than a stray jar.
func (s *Server) handleAddPlugin(w http.ResponseWriter, r *http.Request) {
	if !s.pluginsAvailable(w) {
		return
	}

	var req pluginRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}
	if !s.knownTokenID(req.TokenID) {
		writeError(w, http.StatusBadRequest, "没有这个令牌")
		return
	}

	item, err := s.plugins.Library().Add(req.Name, req.source(), req.TargetDir, req.Note)
	if err != nil {
		s.writePluginError(w, err)
		return
	}
	s.log.Info("plugin added to library", "plugin", item.ID, "repo", item.Source.Repo)

	// A first check costs one API call and turns a freshly added plugin into
	// one the operator can install immediately, instead of a card that says
	// nothing until they click again.
	if _, err := s.plugins.Check(r.Context(), item.ID); err != nil {
		s.log.Warn("first plugin check failed", "plugin", item.ID, "err", err)
	}
	if checked, err := s.plugins.Library().Get(item.ID); err == nil {
		item = checked
	}
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) handleUpdatePlugin(w http.ResponseWriter, r *http.Request) {
	if !s.pluginsAvailable(w) {
		return
	}

	var req pluginRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}
	if !s.knownTokenID(req.TokenID) {
		writeError(w, http.StatusBadRequest, "没有这个令牌")
		return
	}

	item, err := s.plugins.Library().Edit(r.PathValue("id"), req.Name, req.source(), req.TargetDir, req.Note)
	if err != nil {
		s.writePluginError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

// handleDeletePlugin stops tracking a plugin and deletes its downloads. The
// copies instances were given are untouched, which is why this needs no running
// check: it cannot pull a jar out from under a live server.
func (s *Server) handleDeletePlugin(w http.ResponseWriter, r *http.Request) {
	if !s.pluginsAvailable(w) {
		return
	}
	id := r.PathValue("id")
	if err := s.plugins.Library().Remove(id); err != nil {
		s.writePluginError(w, err)
		return
	}
	s.log.Info("plugin removed from library", "plugin", id)
	w.WriteHeader(http.StatusNoContent)
}

// handlePluginReleases lists what a plugin could be installed at. It always
// hits the network, so it is a click rather than something the page does on
// load — see Plugin.Latest for why the cached answer exists.
func (s *Server) handlePluginReleases(w http.ResponseWriter, r *http.Request) {
	if !s.pluginsAvailable(w) {
		return
	}
	releases, err := s.plugins.Releases(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writePluginError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, releases)
}

func (s *Server) handleCheckPlugin(w http.ResponseWriter, r *http.Request) {
	if !s.pluginsAvailable(w) {
		return
	}
	item, err := s.plugins.Check(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writePluginError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

// handleCheckPlugins refreshes every plugin. Failures are recorded per plugin
// rather than failing the request: one renamed repository should not hide the
// updates the other nineteen plugins have.
func (s *Server) handleCheckPlugins(w http.ResponseWriter, r *http.Request) {
	if !s.pluginsAvailable(w) {
		return
	}
	s.plugins.CheckAll(r.Context())
	writeJSON(w, http.StatusOK, s.pluginLibrary())
}

type pluginDownloadRequest struct {
	// Tag names the release to fetch. Empty means whatever is newest, which is
	// what the update button asks for.
	Tag string `json:"tag"`
	// Asset names which jar of that release, by file name. Empty means the
	// primary one, which is what a release publishing a single jar has and
	// what the list page's 更新入库 asks for. A release that ships one build
	// per platform is what this is for: paper and velocity are one version and
	// two files, and which of them you want is not something the panel can
	// work out from the plugin alone.
	Asset string `json:"asset,omitempty"`
}

func (s *Server) handleDownloadPlugin(w http.ResponseWriter, r *http.Request) {
	if !s.pluginsAvailable(w) {
		return
	}

	var req pluginDownloadRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}

	job, err := s.plugins.Start(r.PathValue("id"), strings.TrimSpace(req.Tag), strings.TrimSpace(req.Asset))
	if err != nil {
		s.writePluginError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, job)
}

func (s *Server) handleCancelPluginDownload(w http.ResponseWriter, _ *http.Request) {
	if !s.pluginsAvailable(w) {
		return
	}
	if err := s.plugins.Cancel(); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleDeletePluginVersion removes one downloaded release, or one jar of it.
//
// The tag arrives as a query parameter rather than a path segment because tags
// contain slashes often enough ("release/1.2.0") that a segment would mangle
// them. An optional sha narrows it to a single artifact, which is what deleting
// the Velocity build of a release while keeping the Paper one means.
func (s *Server) handleDeletePluginVersion(w http.ResponseWriter, r *http.Request) {
	if !s.pluginsAvailable(w) {
		return
	}
	tag := r.URL.Query().Get("tag")
	if tag == "" {
		writeError(w, http.StatusBadRequest, "tag is required")
		return
	}

	id := r.PathValue("id")
	var err error
	if sha := r.URL.Query().Get("sha"); sha != "" {
		err = s.plugins.Library().RemoveArtifact(id, tag, sha)
	} else {
		err = s.plugins.Library().RemoveVersion(id, tag)
	}
	if err != nil {
		s.writePluginError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// sourcePreview is what the panel can see of a repository before anybody
// commits to tracking it.
//
// Adding a GitHub source used to be a form you filled in and found out about
// afterwards: a typo, a private repository with no token, a repository that
// publishes source tarballs and no jar, and an asset pattern guessed from
// nothing all failed the same way — an entry in the library that says
// "检查更新失败" and gives no clue which of the four it was. One API call
// answers all of them, and the operator gets to look before agreeing.
type sourcePreview struct {
	Repo string `json:"repo"`
	// Reachable is the headline: could the panel read this repository at all.
	Reachable bool   `json:"reachable"`
	Private   bool   `json:"private"`
	Error     string `json:"error,omitempty"`
	// NeedsToken marks the failure a token would fix, which is the one worth
	// offering a way out of rather than just reporting.
	NeedsToken bool `json:"needsToken,omitempty"`

	// Release is the newest one the panel would see with these settings —
	// which is not the newest release when 包含预发布 is off.
	Release     string `json:"release,omitempty"`
	Version     string `json:"version,omitempty"`
	PublishedAt string `json:"publishedAt,omitempty"`
	// Assets are the jars in that release. More than one is the case the
	// pattern exists for, and seeing them is what makes it fillable.
	Assets []previewAsset `json:"assets"`
	// Picked is the jar the panel would take today, given Pattern. Naming it
	// is the difference between a rule you can check and a rule you hope about.
	Picked  string `json:"picked,omitempty"`
	Pattern string `json:"pattern,omitempty"`
	// Suggest is a pattern the panel would propose when a release ships
	// several jars and none was specified. A suggestion, never applied on its
	// own — which of four platform builds you want is not the panel's call.
	Suggest string `json:"suggest,omitempty"`
	// Releases is how many the panel can see, so "1 个 Release" reads as the
	// warning it is on a repository that should have twenty.
	Releases int `json:"releases"`
}

type previewAsset struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
}

// handlePreviewPluginSource looks at a repository without tracking it.
func (s *Server) handlePreviewPluginSource(w http.ResponseWriter, r *http.Request) {
	if !s.pluginsAvailable(w) {
		return
	}
	repo := strings.TrimSpace(r.URL.Query().Get("repo"))
	if repo == "" {
		writeError(w, http.StatusBadRequest, "repo is required")
		return
	}

	src := plugin.Source{
		Kind:         plugin.SourceGitHub,
		Repo:         repo,
		AssetPattern: strings.TrimSpace(r.URL.Query().Get("pattern")),
		Prerelease:   r.URL.Query().Get("prerelease") == "true",
		// Which credential to look with is part of the question: a repository
		// one token cannot see is a 404, and the whole point of the preview is
		// to find that out before the source is added rather than after.
		TokenID: strings.TrimSpace(r.URL.Query().Get("tokenId")),
	}
	if !s.knownTokenID(src.TokenID) {
		writeError(w, http.StatusBadRequest, "没有这个令牌")
		return
	}
	normalised, err := src.Normalise()
	if err != nil {
		writeJSON(w, http.StatusOK, sourcePreview{Repo: repo, Error: err.Error(), Assets: []previewAsset{}})
		return
	}

	preview := sourcePreview{Repo: normalised.Repo, Pattern: normalised.AssetPattern, Assets: []previewAsset{}}
	if private, err := s.plugins.Client().Visibility(r.Context(), normalised); err == nil {
		preview.Reachable, preview.Private = true, private
	}

	releases, err := s.plugins.Client().Releases(r.Context(), normalised)
	if err != nil {
		preview.Error = err.Error()
		preview.NeedsToken = errors.Is(err, plugin.ErrNeedsToken)
		writeJSON(w, http.StatusOK, preview)
		return
	}

	preview.Reachable = true
	preview.Releases = len(releases)
	newest := releases[0]
	preview.Release, preview.Version = newest.Tag, newest.Version
	preview.PublishedAt = newest.PublishedAt.Format(time.RFC3339)
	preview.Picked = newest.Asset.Name
	for _, asset := range newest.Assets {
		preview.Assets = append(preview.Assets, previewAsset{Name: asset.Name, Size: asset.Size})
	}
	if len(preview.Assets) > 1 && preview.Pattern == "" {
		preview.Suggest = suggestPattern(newest.Asset.Name)
	}
	writeJSON(w, http.StatusOK, preview)
}

// suggestPattern turns the jar the panel would pick into a rule that keeps
// picking it as the version number moves.
//
// The version is the part that changes, so it becomes the wildcard —
// LuckPerms-Bukkit-5.5.71.jar proposes LuckPerms-Bukkit-*.jar, which goes on
// matching next month and goes on *not* matching the velocity build beside it.
// Only offered, never applied: which of four platform builds a server wants is
// not something the panel can work out from the file names.
func suggestPattern(name string) string {
	base := strings.TrimSuffix(name, ".jar")
	parts := strings.Split(base, "-")
	for i := len(parts) - 1; i >= 1; i-- {
		if strings.ContainsAny(parts[i], "0123456789") {
			return strings.Join(parts[:i], "-") + "-*.jar"
		}
	}
	return ""
}

// pluginPolicyRequest is the 设置 tab of a plugin's drawer, whole. Sent as one
// object because these settings are read together and only make sense together
// — a retention count means something different under 自动入库 than under 手动.
type pluginPolicyRequest struct {
	Update          string `json:"update"`
	Pin             string `json:"pin"`
	Keep            int    `json:"keep"`
	AllowSelfUpdate bool   `json:"allowSelfUpdate"`
}

func (s *Server) handlePluginPolicy(w http.ResponseWriter, r *http.Request) {
	if !s.pluginsAvailable(w) {
		return
	}

	var req pluginPolicyRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}
	mode := plugin.UpdateMode(strings.TrimSpace(req.Update))
	switch mode {
	case plugin.UpdateManual, plugin.UpdateNotify, plugin.UpdateFetch, plugin.UpdatePush:
	default:
		writeError(w, http.StatusBadRequest, "unknown update mode")
		return
	}

	item, err := s.plugins.Library().SetPolicy(r.PathValue("id"), plugin.Policy{
		Update:          mode,
		Pin:             strings.TrimSpace(req.Pin),
		Keep:            req.Keep,
		AllowSelfUpdate: req.AllowSelfUpdate,
	})
	if err != nil {
		s.writePluginError(w, err)
		return
	}
	s.log.Info("plugin policy changed", "plugin", item.ID,
		"update", mode, "pin", item.Policy.Pin, "keep", item.Policy.Keep,
		"selfUpdate", item.Policy.AllowSelfUpdate)
	writeJSON(w, http.StatusOK, item)
}

// ------------------------------------------------------- per-instance

type instancePluginsResponse struct {
	Entries []plugin.Entry `json:"entries"`
	// Library travels with the listing so the instance page can offer the
	// versions on hand without a second request — the two are always read
	// together, and the picker is useless without both.
	Library []pluginView `json:"library"`
	Root    string       `json:"root"`
	// Target is what this server turned out to be — the basis of every
	// compatibility badge on the page, and worth showing so an operator can
	// tell "未知兼容性" caused by a plugin from "未知兼容性" caused by the panel
	// not recognising their server jar.
	Target plugin.Target `json:"target"`
	// Pending are the changes the running process has not seen. Empty for a
	// stopped server: there is nothing running that could have missed them.
	Pending []pendingChange `json:"pending"`
	// Live says whether there is a process to restart, which is what decides
	// between "待重启生效" and "下次启动时生效".
	Live bool `json:"live"`
	// Failures are the plugins the server said it could not load, read out of
	// this boot's console output. Sent alongside the entries as well as
	// attached to them, so a failure naming a plugin that is no longer in the
	// directory is still reported rather than silently dropped.
	Failures []plugin.Failure `json:"failures"`
	// LogAvailable is false when the server has not run since the panel
	// started, in which case an empty Failures list means "nothing to read"
	// rather than "nothing wrong".
	LogAvailable bool `json:"logAvailable"`
}

// pendingChange is one change awaiting a restart, with the label already
// written — the phrasing depends on the action and belongs next to the actions.
type pendingChange struct {
	plugin.Change
	Label string `json:"label"`
}

// handleListInstancePlugins answers everything one server's plugin page shows.
//
// Three sources joined here rather than in the browser: the directory, the
// panel's install records, and what the server printed while starting. The
// join has to happen where all three are, and the third is the one that turns
// this page from an inventory into something that reports problems — a
// Minecraft server that cannot load a plugin says so once, in the log, and
// then carries on as if nothing happened.
func (s *Server) handleListInstancePlugins(w http.ResponseWriter, r *http.Request) {
	inst, ok := s.instanceFromPath(w, r)
	if !ok {
		return
	}
	if s.plugins == nil || s.instancePlugins == nil {
		writeJSON(w, http.StatusOK, instancePluginsResponse{
			Entries: []plugin.Entry{}, Library: []pluginView{},
			Pending: []pendingChange{}, Failures: []plugin.Failure{},
		})
		return
	}

	cfg := inst.Config()
	entries, err := s.instancePlugins.List(cfg.ID, cfg.Directory)
	if err != nil {
		s.writePluginError(w, err)
		return
	}

	status := inst.Status()
	var startedAt time.Time
	if status.StartedAt != nil {
		startedAt = *status.StartedAt
	}

	resp := instancePluginsResponse{
		Entries:      entries,
		Library:      s.pluginLibrary().Plugins,
		Root:         cfg.Directory,
		Target:       s.detectTarget(cfg),
		Pending:      []pendingChange{},
		Live:         status.State.Running(),
		Failures:     s.scanPluginFailures(inst, startedAt),
		LogAvailable: !startedAt.IsZero(),
	}

	for _, change := range s.pendingChanges(cfg.ID, startedAt) {
		resp.Pending = append(resp.Pending, pendingChange{Change: change, Label: change.Label()})
	}

	pendingByKey := map[string]string{}
	for _, change := range resp.Pending {
		pendingByKey[change.Key] = change.Action
	}

	for i := range resp.Entries {
		entry := &resp.Entries[i]
		verdict := plugin.Judge(resp.Target, entry.Loaders, entry.GameVersions)
		entry.Compat = &verdict
		entry.PendingAction = pendingByKey[entry.Key]
		// What this server could move up to — judged against this server, not
		// against the top of the library's list. A plugin that publishes a
		// proxy build after its server build has a newer version that is not a
		// newer version *here*.
		if entry.Managed && entry.PluginID != "" {
			if item, err := s.plugins.Library().Get(entry.PluginID); err == nil {
				entry.Update = plugin.UpdateFor(item, entry.Tag, resp.Target)
			}
		}
		// Both names, because which one the server printed depends on how far
		// the load got: the jar's if it never read plugin.yml, the declared
		// name if it did.
		entry.Failure = plugin.MatchFailure(resp.Failures,
			entry.Name, strings.TrimSuffix(entry.FileName, ".jar"), jarInfoName(entry))
	}

	// Problems first. An operator opens this page because something is wrong
	// far more often than to admire the inventory, and a red row eleventh in
	// an alphabetical list is a red row nobody sees.
	sort.SliceStable(resp.Entries, func(a, b int) bool {
		if left, right := resp.Entries[a].Failure != nil, resp.Entries[b].Failure != nil; left != right {
			return left
		}
		if left, right := resp.Entries[a].Managed, resp.Entries[b].Managed; left != right {
			return left
		}
		return strings.ToLower(resp.Entries[a].Name) < strings.ToLower(resp.Entries[b].Name)
	})
	writeJSON(w, http.StatusOK, resp)
}

// entryName is what a listing calls one row, for a pending-change label. The
// key falls through when the row has already gone, which is the removal case.
func entryName(entries []plugin.Entry, key string) string {
	for _, entry := range entries {
		if entry.Key == key {
			return entry.Name
		}
	}
	return strings.TrimPrefix(strings.TrimPrefix(key, "plugin:"), "file:")
}

func jarInfoName(entry *plugin.Entry) string {
	if entry.Jar == nil {
		return ""
	}
	return entry.Jar.Name
}

// scanPluginFailures reads this boot's console output for plugins that did not
// load.
//
// Scoped to the current process by timestamp. The ring buffer holds the last
// two thousand lines whatever produced them, so a server restarted after a fix
// would otherwise keep showing the failure it was restarted to clear — which
// is exactly the moment an operator is looking at this page.
func (s *Server) scanPluginFailures(inst *instance.Instance, startedAt time.Time) []plugin.Failure {
	if startedAt.IsZero() {
		return []plugin.Failure{}
	}
	all := inst.LinesSince(0)
	lines := make([]string, 0, len(all))
	for _, line := range all {
		if line.Time.Before(startedAt) {
			continue
		}
		lines = append(lines, line.Text)
	}
	failures := plugin.ScanFailures(lines)
	if failures == nil {
		return []plugin.Failure{}
	}
	return failures
}

// pendingChanges is the pending list for an instance, or nothing when the
// store was not wired up.
func (s *Server) pendingChanges(instanceID string, startedAt time.Time) []plugin.Change {
	if s.pendingPlugins == nil {
		return nil
	}
	return s.pendingPlugins.Since(instanceID, startedAt)
}

// recordPending notes a change against an instance. Failures are logged and
// swallowed: the jar is already where it belongs, and refusing an install
// because the banner could not be written would be the wrong trade.
func (s *Server) recordPending(instanceID string, change plugin.Change) {
	if s.pendingPlugins == nil {
		return
	}
	if err := s.pendingPlugins.Record(instanceID, change); err != nil {
		s.log.Warn("could not record a pending plugin change",
			"instance", instanceID, "plugin", change.Name, "err", err)
	}
}

type installPluginRequest struct {
	PluginID string `json:"pluginId"`
	// Tag is the library version to install. It is required: "whichever" is
	// exactly the ambiguity a pinned plugin version exists to remove.
	Tag string `json:"tag"`
	// SHA picks which jar of that release, for a plugin that ships one build
	// per platform. Empty means the release's primary jar, which is the right
	// answer for the great majority that ship exactly one.
	SHA string `json:"sha,omitempty"`
}

// handleInstallInstancePlugin copies a library version into an instance, or
// swaps the version of one it already has.
//
// A copy rather than a link: the instance owns its jar, so deleting the library
// entry cannot change what a running server has loaded.
func (s *Server) handleInstallInstancePlugin(w http.ResponseWriter, r *http.Request) {
	inst, ok := s.instanceFromPath(w, r)
	if !ok {
		return
	}
	if !s.pluginsAvailable(w) {
		return
	}

	var req installPluginRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}
	if strings.TrimSpace(req.PluginID) == "" || strings.TrimSpace(req.Tag) == "" {
		writeError(w, http.StatusBadRequest, "pluginId and tag are required")
		return
	}

	cfg := inst.Config()
	entry, snapshot, err := s.instancePlugins.InstallArtifact(
		cfg.ID, cfg.Directory, req.PluginID, req.Tag, s.jarFor(cfg, req.PluginID, req.Tag, req.SHA),
		actorOf(r))
	if err != nil {
		s.writePluginError(w, err)
		return
	}
	// The jar is in place and the running server will not read it until it
	// restarts. Saying so is the point of the record — see plugin.Pending.
	s.recordPending(cfg.ID, plugin.Change{
		Key:    entry.Key,
		Name:   entry.Name,
		Action: entry.PendingAction,
		At:     time.Now(),
	})
	s.log.Info("plugin installed into instance",
		"instance", cfg.Name, "plugin", req.PluginID, "tag", entry.Tag,
		"swept", len(snapshot.Removed), "backup", snapshot.BackupDir)
	writeJSON(w, http.StatusOK, installResponse{Entry: entry, Snapshot: snapshot})
}

// installResponse carries the transaction alongside its result.
//
// The snapshot is not bookkeeping the caller can ignore: it says which jars
// were swept out of the directory, whether the config was backed up, and
// therefore what a rollback would and would not restore. A dialog that reported
// "installed" without any of that would be describing a file copy, and this is
// not one.
type installResponse struct {
	Entry    plugin.Entry    `json:"entry"`
	Snapshot plugin.Snapshot `json:"snapshot"`
}

type rollbackPluginRequest struct {
	PluginID string `json:"pluginId"`
	// WithConfig restores the plugin's config directory as well as its jar.
	// Off by default: a plugin that has been running since the upgrade has
	// written data since, and throwing that away is a separate decision from
	// going back to the old build.
	WithConfig bool `json:"withConfig"`
}

// handleRollbackInstancePlugin puts back the version this instance was on
// before its last upgrade.
//
// It reads the snapshot, not the library. The whole reason to keep old versions
// is to be able to undo, and a rollback that depended on the library still
// holding the old jar would fail on exactly the panel where somebody had
// tidied up — which is the panel where a retention policy is doing its job.
func (s *Server) handleRollbackInstancePlugin(w http.ResponseWriter, r *http.Request) {
	inst, ok := s.instanceFromPath(w, r)
	if !ok {
		return
	}
	if !s.pluginsAvailable(w) {
		return
	}

	var req rollbackPluginRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}
	if strings.TrimSpace(req.PluginID) == "" {
		writeError(w, http.StatusBadRequest, "pluginId is required")
		return
	}

	cfg := inst.Config()
	entry, err := s.instancePlugins.Rollback(cfg.ID, cfg.Directory, req.PluginID, req.WithConfig)
	if err != nil {
		s.writePluginError(w, err)
		return
	}
	s.recordPending(cfg.ID, plugin.Change{
		Key: entry.Key, Name: entry.Name, Action: entry.PendingAction, At: time.Now(),
	})
	s.log.Info("instance plugin rolled back",
		"instance", cfg.Name, "plugin", req.PluginID, "to", entry.Version, "config", req.WithConfig)
	writeJSON(w, http.StatusOK, entry)
}

// handleAcceptInstancePlugin records the file on disk as the new baseline.
//
// The second of the two answers a drift finding has. "Restore the library's
// copy" is right when the file was tampered with; this is right when it was
// not, and a panel that only offered the first would leave an operator whose
// anticheat updated itself with a permanent warning and no way to clear it
// except by overwriting a file they wanted.
func (s *Server) handleAcceptInstancePlugin(w http.ResponseWriter, r *http.Request) {
	inst, ok := s.instanceFromPath(w, r)
	if !ok {
		return
	}
	if !s.pluginsAvailable(w) {
		return
	}

	var req rollbackPluginRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}
	if strings.TrimSpace(req.PluginID) == "" {
		writeError(w, http.StatusBadRequest, "pluginId is required")
		return
	}

	cfg := inst.Config()
	entry, err := s.instancePlugins.Accept(cfg.ID, cfg.Directory, req.PluginID)
	if err != nil {
		s.writePluginError(w, err)
		return
	}
	s.log.Info("instance plugin drift accepted",
		"instance", cfg.Name, "plugin", req.PluginID, "sha", entry.SHA256)
	writeJSON(w, http.StatusOK, entry)
}

// handleReconcileInstancePlugins hashes the server's plugin directory and
// compares it with the panel's ledger.
//
// Its own route rather than part of the listing because it is the expensive
// one: every jar on the server read end to end. That is affordable when
// somebody asks for it, when the server starts, and after an upgrade — and it
// is not affordable on every page load, which is what it would become if the
// listing did it.
func (s *Server) handleReconcileInstancePlugins(w http.ResponseWriter, r *http.Request) {
	inst, ok := s.instanceFromPath(w, r)
	if !ok {
		return
	}
	if !s.pluginsAvailable(w) {
		return
	}

	cfg := inst.Config()
	report, err := s.instancePlugins.Reconcile(cfg.ID, cfg.Directory)
	if err != nil {
		s.writePluginError(w, err)
		return
	}
	if !report.Clean() {
		s.log.Info("plugin reconciliation found differences", "instance", cfg.Name,
			"drift", report.Drift, "missing", report.Missing, "foreign", report.Foreign)
	}
	writeJSON(w, http.StatusOK, report)
}

// actorOf names whoever asked, for the upgrade log. A fleet with more than one
// operator needs the snapshot to say which of them moved production.
func actorOf(r *http.Request) string {
	who, _ := principalFrom(r.Context())
	return who.username
}

// importedResult is one uploaded jar's outcome, per file rather than per
// request: a five-jar upload where the third one is a zip should still land
// the other four and name the one it could not read.
type importedResult struct {
	FileName string           `json:"fileName"`
	Imported *plugin.Imported `json:"imported,omitempty"`
	Error    string           `json:"error,omitempty"`
}

// handleImportPlugins takes jars the operator uploaded into the library.
//
// The way in for everything the catalogues cannot reach: a marketplace plugin,
// a build from a fork, something a friend sent over. Those jars could always be
// dropped into a server's plugins directory through the file manager — this is
// the difference between doing that five times and doing it once.
//
// Streamed part by part rather than parsed into memory: plugin jars run to tens
// of megabytes and the panel is expected to share a small box with the servers
// it runs. Each part is staged, checksummed, and asked what it is; see
// plugin/importer.go.
func (s *Server) handleImportPlugins(w http.ResponseWriter, r *http.Request) {
	if !s.pluginsAvailable(w) {
		return
	}

	// Optional: with an id, every jar joins that plugin. Without one, each jar
	// finds or creates its own entry from the name it declares.
	id := strings.TrimSpace(r.URL.Query().Get("plugin"))

	reader, err := r.MultipartReader()
	if err != nil {
		writeError(w, http.StatusBadRequest, "expected a multipart upload")
		return
	}

	limit := s.maxUploadBytes()
	results := make([]importedResult, 0, 2)
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			writeError(w, http.StatusBadRequest, "malformed upload")
			return
		}
		if part.FileName() == "" {
			part.Close()
			continue
		}

		imported, importErr := s.plugins.Library().ImportJar(id, part.FileName(), part, limit)
		part.Close()

		if importErr != nil {
			results = append(results, importedResult{FileName: part.FileName(), Error: importErr.Error()})
			s.log.Warn("plugin import failed", "file", part.FileName(), "err", importErr)
			continue
		}
		results = append(results, importedResult{FileName: part.FileName(), Imported: &imported})
		s.log.Info("plugin imported",
			"plugin", imported.Plugin.ID, "version", imported.Version.Version, "file", part.FileName())
	}

	if len(results) == 0 {
		writeError(w, http.StatusBadRequest, "no files in the upload")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"results": results})
}

type adoptPluginRequest struct {
	// Key is the unmanaged row to adopt, as handed out by the listing.
	Key string `json:"key"`
}

// handleAdoptInstancePlugin starts tracking a jar the panel found rather than
// installed, when it turns out to be one of the library's own downloads.
//
// Nothing on disk changes: the file is already there and already loading. What
// it adds is the record of which plugin and version it is, which is what makes
// updating and rolling it back possible afterwards.
func (s *Server) handleAdoptInstancePlugin(w http.ResponseWriter, r *http.Request) {
	inst, ok := s.instanceFromPath(w, r)
	if !ok {
		return
	}
	if !s.pluginsAvailable(w) {
		return
	}

	var req adoptPluginRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}
	if strings.TrimSpace(req.Key) == "" {
		writeError(w, http.StatusBadRequest, "key is required")
		return
	}

	cfg := inst.Config()
	entry, err := s.instancePlugins.Adopt(cfg.ID, cfg.Directory, req.Key)
	if err != nil {
		s.writePluginError(w, err)
		return
	}
	s.log.Info("instance plugin adopted",
		"instance", cfg.Name, "plugin", entry.PluginID, "tag", entry.Tag)
	writeJSON(w, http.StatusOK, entry)
}

type togglePluginRequest struct {
	Key     string `json:"key"`
	Enabled bool   `json:"enabled"`
}

// handleToggleInstancePlugin switches a plugin on or off for one instance by
// renaming its jar. It takes effect on the server's next start, which is what
// the UI says next to the switch.
func (s *Server) handleToggleInstancePlugin(w http.ResponseWriter, r *http.Request) {
	inst, ok := s.instanceFromPath(w, r)
	if !ok {
		return
	}
	if !s.pluginsAvailable(w) {
		return
	}

	var req togglePluginRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}

	cfg := inst.Config()
	if err := s.instancePlugins.SetEnabled(cfg.ID, cfg.Directory, req.Key, req.Enabled); err != nil {
		s.writePluginError(w, err)
		return
	}
	s.log.Info("instance plugin toggled",
		"instance", cfg.Name, "plugin", req.Key, "enabled", req.Enabled)

	entries, err := s.instancePlugins.List(cfg.ID, cfg.Directory)
	if err != nil {
		s.writePluginError(w, err)
		return
	}

	// Switching a plugin off renames its jar; the server that already loaded
	// it goes on running it until it restarts. This is the case the panel used
	// to get wrong with an on/off switch that looked immediate.
	action := plugin.ActionEnable
	if !req.Enabled {
		action = plugin.ActionDisable
	}
	s.recordPending(cfg.ID, plugin.Change{
		Key:    req.Key,
		Name:   entryName(entries, req.Key),
		Action: action,
		At:     time.Now(),
	})
	writeJSON(w, http.StatusOK, entries)
}

func (s *Server) handleUninstallInstancePlugin(w http.ResponseWriter, r *http.Request) {
	inst, ok := s.instanceFromPath(w, r)
	if !ok {
		return
	}
	if !s.pluginsAvailable(w) {
		return
	}

	key := r.URL.Query().Get("key")
	if key == "" {
		writeError(w, http.StatusBadRequest, "key is required")
		return
	}

	cfg := inst.Config()
	// Read before the removal, or there is no row left to take the name from.
	name := key
	if entries, err := s.instancePlugins.List(cfg.ID, cfg.Directory); err == nil {
		name = entryName(entries, key)
	}

	if err := s.instancePlugins.Uninstall(cfg.ID, cfg.Directory, key); err != nil {
		s.writePluginError(w, err)
		return
	}
	s.recordPending(cfg.ID, plugin.Change{
		Key: key, Name: name, Action: plugin.ActionRemove, At: time.Now(),
	})
	s.log.Info("plugin removed from instance", "instance", cfg.Name, "plugin", key)
	w.WriteHeader(http.StatusNoContent)
}
