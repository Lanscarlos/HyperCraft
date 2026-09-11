package api

import (
	"net/http"

	"github.com/lanscarlos/hypercraft/internal/authz"
)

// The route table.
//
// Routes are a value rather than a run of mux.HandleFunc calls for one reason:
// a route that forgets to say what it requires is a silent hole in the
// permission system, and a hole of that shape is invisible in review — the
// handler looks right, the path looks right, and nothing is missing until
// somebody reaches it. A table can be walked by a test, and a new entry cannot
// be written without a capability field staring back at whoever adds it.
//
// See docs/proposal-multi-user.md. Nothing here is enforced yet: the panel
// still has one operator, who holds everything. This is the declaration the
// enforcement will read.
type route struct {
	// pattern is an http.ServeMux pattern, method included.
	pattern string
	handler http.HandlerFunc
	// need lists every capability a request must hold, all of them required.
	//
	// More than one means the route crosses more than one boundary, which is
	// commoner than it looks: installing a library plugin into a server touches
	// both the shared library and that server. The rule is to name every
	// boundary crossed, not the most obvious one.
	//
	// Empty only in publicRoutes. Routes open to every signed-in user say so
	// with authz.CapSignedIn rather than by leaving this blank.
	need []authz.Cap
}

// rt builds a table entry. The capabilities come last so the usual single one
// reads as a suffix on the line.
func rt(pattern string, handler http.HandlerFunc, need ...authz.Cap) route {
	return route{pattern: pattern, handler: handler, need: need}
}

// publicRoutes are reachable without any credential. The set is pinned by a
// test: adding to it has to be a deliberate act, because "it needs to work
// before login" is exactly the argument that would otherwise let a route slip
// outside the door one at a time.
func (s *Server) publicRoutes() []route {
	return []route{
		rt("POST /api/auth/login", s.handleLogin),
		rt("GET /api/health", s.handleHealth),
		// Pairing is authenticated by the password rather than by a session, so
		// it sits outside requireAuth. See handleCreateDevice for why.
		rt("POST /api/auth/devices", s.handleCreateDevice),
	}
}

// protectedRoutes are everything behind requireAuth.
func (s *Server) protectedRoutes() []route {
	return []route{
		// The operator's own account. Not a capability an operator grants:
		// every signed-in user can sign out and change their own password.
		// Listing and revoking devices will be filtered to the caller's own
		// once devices belong to users.
		rt("POST /api/auth/logout", s.handleLogout, authz.CapSignedIn),
		rt("GET /api/auth/me", s.handleMe, authz.CapSignedIn),
		rt("POST /api/auth/password", s.handleChangePassword, authz.CapSignedIn),
		rt("GET /api/auth/devices", s.handleListDevices, authz.CapSignedIn),
		rt("DELETE /api/auth/devices/{id}", s.handleDeleteDevice, authz.CapSignedIn),
		// The credential trail is panel-wide rather than personal: it is the
		// page an operator opens to find out who has been knocking.
		rt("GET /api/auth/events", s.handleAuthEvents, authz.CapPanelSecurity),

		// Accounts and roles. All one capability, and a dangerous one: whoever
		// can edit a role can put themselves in one that holds everything.
		// Splitting it into read and write would suggest a boundary that is not
		// there — seeing the list is most of the way to editing it, because the
		// thing you would do with the list is grant yourself something.
		rt("GET /api/users", s.handleListUsers, authz.CapPanelUsers),
		rt("POST /api/users", s.handleCreateUser, authz.CapPanelUsers),
		rt("PUT /api/users/{id}", s.handleUpdateUser, authz.CapPanelUsers),
		rt("POST /api/users/{id}/password", s.handleSetUserPassword, authz.CapPanelUsers),
		rt("POST /api/users/{id}/signout", s.handleSignOutUser, authz.CapPanelUsers),
		rt("DELETE /api/users/{id}", s.handleDeleteUser, authz.CapPanelUsers),

		rt("GET /api/roles", s.handleListRoles, authz.CapPanelUsers),
		rt("POST /api/roles", s.handleCreateRole, authz.CapPanelUsers),
		rt("PUT /api/roles/{id}", s.handleUpdateRole, authz.CapPanelUsers),
		rt("DELETE /api/roles/{id}", s.handleDeleteRole, authz.CapPanelUsers),
		// The vocabulary the role editor is built from. Behind the same
		// capability as the editor: on its own it is only a list of names, but
		// it is a list of exactly what there is to grant.
		rt("GET /api/capabilities", s.handleCapabilities, authz.CapPanelUsers),

		rt("GET /api/instances", s.handleListInstances, authz.CapInstanceView),
		rt("POST /api/instances", s.handleCreateInstance, authz.CapPanelCreate),
		rt("GET /api/instances/{id}", s.handleGetInstance, authz.CapInstanceView),
		// One route, two very different powers: renaming a server and pointing
		// it at a different directory with a different launch command arrive in
		// the same body. It declares the harmless one, and the dangerous fields
		// — directory, java, jar, jvmArgs, serverArgs, command — will be
		// rejected without CapInstanceLaunch when enforcement lands. Splitting
		// the route instead would break every client that saves the whole form.
		rt("PUT /api/instances/{id}", s.handleUpdateInstance, authz.CapInstanceSettings),
		rt("DELETE /api/instances/{id}", s.handleDeleteInstance, authz.CapInstanceDelete),

		rt("POST /api/instances/{id}/start", s.handlePower(powerStart), authz.CapInstancePower),
		rt("POST /api/instances/{id}/stop", s.handlePower(powerStop), authz.CapInstancePower),
		rt("POST /api/instances/{id}/restart", s.handlePower(powerRestart), authz.CapInstancePower),
		rt("POST /api/instances/{id}/kill", s.handlePower(powerKill), authz.CapInstancePower),
		rt("POST /api/instances/{id}/command", s.handleCommand, authz.CapInstanceConsole),

		rt("GET /api/instances/{id}/logs", s.handleLogs, authz.CapInstanceView),
		rt("GET /api/instances/{id}/properties", s.handleGetProperties, authz.CapInstanceView),
		rt("PUT /api/instances/{id}/properties", s.handlePutProperties, authz.CapInstanceConfig),
		rt("GET /api/instances/{id}/eula", s.handleGetEULA, authz.CapInstanceView),
		rt("POST /api/instances/{id}/eula", s.handleAcceptEULA, authz.CapInstanceConfig),

		// What the panel expects to happen when this instance is started. Read
		// only: it reports, and the one repair it offers is a separate route,
		// because adding an execute bit to a file in the server directory is a
		// write to that directory and nothing less.
		rt("GET /api/instances/{id}/launch-check", s.handleLaunchCheck, authz.CapInstanceView),
		rt("POST /api/instances/{id}/launch-check/fix", s.handleLaunchFix, authz.CapInstanceFilesWrite),

		// Forge's user_jvm_args.txt, which is where the heap of a
		// script-launched server lives. Two boundaries, so both are named: it
		// writes a config file in the server directory, and what it writes is
		// a launch setting — the same -Xmx that needs CapInstanceLaunch when
		// it arrives through the instance config instead.
		rt("GET /api/instances/{id}/jvm-args", s.handleGetJVMArgs, authz.CapInstanceView),
		rt("PUT /api/instances/{id}/jvm-args", s.handlePutJVMArgs,
			authz.CapInstanceConfig, authz.CapInstanceLaunch),

		// The settings that live outside server.properties: bukkit.yml,
		// spigot.yml and whichever layout of Paper's config this server reads.
		rt("GET /api/instances/{id}/configs", s.handleGetServerConfigs, authz.CapInstanceView),
		rt("PUT /api/instances/{id}/configs/{file}", s.handlePutServerConfig, authz.CapInstanceConfig),

		// The proxy's own configuration. Beside the properties routes rather
		// than folded into them: velocity.toml is a different file with
		// different keys, and an instance answers to one pair or the other,
		// never to both.
		rt("GET /api/instances/{id}/velocity", s.handleGetVelocity, authz.CapInstanceView),
		rt("PUT /api/instances/{id}/velocity", s.handlePutVelocity, authz.CapInstanceConfig),

		// Which proxy stands in front of which servers. Panel-wide rather than
		// per-instance because a link is a fact about two instances, and
		// neither of them owns it — which is also why it is one capability
		// rather than an instance one: the two ends can belong to different
		// people, and there is no answer to "whose link is it".
		rt("GET /api/network", s.handleNetwork, authz.CapPanelNetwork),
		rt("POST /api/network/link", s.handleNetworkLink, authz.CapPanelNetwork),
		rt("POST /api/network/repair", s.handleNetworkRepair, authz.CapPanelNetwork),
		rt("POST /api/network/unlink", s.handleNetworkUnlink, authz.CapPanelNetwork),

		// Config history. Two segments deep below the instance for the same
		// reason the plugin config routes are: "commits" must never be
		// reachable as anything else. Notably absent, and staying absent:
		// anything that hands over the repository as a whole. See
		// handlers_confighist.go.
		rt("GET /api/instances/{id}/config-history", s.handleConfigHistory, authz.CapInstanceHistory),
		rt("GET /api/instances/{id}/config-history/commits/{ref}", s.handleConfigHistoryCommit, authz.CapInstanceHistory),
		rt("GET /api/instances/{id}/config-history/diff", s.handleConfigHistoryDiff, authz.CapInstanceHistory),
		rt("GET /api/instances/{id}/config-history/file", s.handleConfigHistoryFile, authz.CapInstanceHistory),
		rt("POST /api/instances/{id}/config-history/snapshot", s.handleConfigHistorySnapshot, authz.CapInstanceHistory),
		rt("POST /api/instances/{id}/config-history/restore/preview", s.handleConfigHistoryRestorePreview, authz.CapInstanceHistory),
		// Restoring writes a config file, which is the thing CapInstanceConfig
		// governs. Reaching it through the timeline instead of the editor does
		// not make it a different act.
		rt("POST /api/instances/{id}/config-history/restore", s.handleConfigHistoryRestore, authz.CapInstanceHistory, authz.CapInstanceConfig),
		rt("POST /api/instances/{id}/config-history/compact", s.handleConfigHistoryCompact, authz.CapInstanceHistory),
		rt("PUT /api/instances/{id}/config-history/settings", s.handleConfigHistorySettings, authz.CapInstanceHistory),

		// Server cores. Panel-wide rather than per-instance, for the same
		// reason Java runtimes are: one download serves every server built from
		// it, and an instance is handed its own copy out of the library.
		rt("GET /api/downloads/projects", s.handleListCoreProjects, authz.CapLibraryCores),
		rt("GET /api/downloads/projects/{project}/versions", s.handleListCoreVersions, authz.CapLibraryCores),
		rt("GET /api/downloads/projects/{project}/versions/{version}/build", s.handleLatestCoreBuild, authz.CapLibraryCores),
		rt("GET /api/cores", s.handleCoreLibrary, authz.CapLibraryCores),
		rt("POST /api/cores", s.handleStartCoreDownload, authz.CapLibraryCores),
		rt("POST /api/cores/cancel", s.handleCancelCoreDownload, authz.CapLibraryCores),
		rt("DELETE /api/cores/{id}", s.handleDeleteCore, authz.CapLibraryCores),
		// Deciding which jar a server runs is a launch setting that happens to
		// be spelled as a copy out of the library, so it needs both.
		rt("POST /api/instances/{id}/core", s.handleApplyCore, authz.CapInstanceLaunch, authz.CapLibraryCores),

		// Plugins. Panel-wide on purpose, and more strictly so than cores are:
		// a plugin is added, versioned and updated here and nowhere else, and
		// an instance may only take a copy, swap which version it holds, or
		// switch one off. Letting every server manage its own downloads is how
		// a panel ends up with six subtly different copies of the same plugin
		// and nobody able to say which is which.
		rt("GET /api/plugins", s.handlePluginLibrary, authz.CapLibraryPlugins),
		rt("POST /api/plugins", s.handleAddPlugin, authz.CapLibraryPlugins),
		rt("POST /api/plugins/check", s.handleCheckPlugins, authz.CapLibraryPlugins),
		rt("POST /api/plugins/import", s.handleImportPlugins, authz.CapLibraryPlugins),
		rt("GET /api/plugins/source/preview", s.handlePreviewPluginSource, authz.CapLibraryPlugins),
		// Discovery. Two segments deep for the same reason the config routes
		// are: "browse" must not be reachable as a plugin id.
		rt("GET /api/plugins/browse", s.handleBrowsePlugins, authz.CapLibraryPlugins),
		rt("GET /api/plugins/browse/{source}/{id}", s.handleBrowsePluginDetail, authz.CapLibraryPlugins),
		rt("POST /api/plugins/browse/track", s.handleTrackPlugin, authz.CapLibraryPlugins),
		// The cross-instance view, and the bulk operation it exists to enable.
		// Both read the whole fleet rather than one server, so the instance
		// grant has to narrow them from inside the handler — there is no {id}
		// in the path to narrow them by. See the proposal's step 3.
		rt("GET /api/plugins/overview", s.handlePluginOverview, authz.CapLibraryPlugins, authz.CapInstanceView),
		rt("POST /api/plugins/bulk/preview", s.handleBulkUpgradePreview, authz.CapLibraryPlugins, authz.CapInstanceView),
		rt("POST /api/plugins/bulk/upgrade", s.handleBulkUpgrade, authz.CapLibraryPlugins, authz.CapInstancePlugins),
		// Two segments deep on purpose: "PUT /api/plugins/{id}" already owns
		// the single-segment shape, and a plugin an operator happened to name
		// "token" would otherwise become the one plugin nobody can edit.
		//
		// These are panel credentials rather than library management, so they
		// answer to the settings capability and not to CapLibraryPlugins.
		rt("PUT /api/plugins/config/token", s.handlePluginToken, authz.CapPanelSettings),
		rt("POST /api/plugins/config/tokens", s.handlePluginTokens, authz.CapPanelSettings),
		rt("PUT /api/plugins/config/tokens/{tokenId}", s.handleUpdatePluginToken, authz.CapPanelSettings),
		rt("DELETE /api/plugins/config/tokens/{tokenId}", s.handleDeletePluginToken, authz.CapPanelSettings),
		rt("PUT /api/plugins/config/mirror", s.handlePluginMirror, authz.CapPanelSettings),
		rt("POST /api/plugins/cancel", s.handleCancelPluginDownload, authz.CapLibraryPlugins),
		rt("DELETE /api/plugins/downloads", s.handleClearPluginDownloads, authz.CapLibraryPlugins),
		rt("PUT /api/plugins/{id}", s.handleUpdatePlugin, authz.CapLibraryPlugins),
		rt("DELETE /api/plugins/{id}", s.handleDeletePlugin, authz.CapLibraryPlugins),
		rt("GET /api/plugins/{id}/releases", s.handlePluginReleases, authz.CapLibraryPlugins),
		// Lists the servers a plugin could go to, so it reads instances too.
		rt("GET /api/plugins/{id}/targets", s.handlePluginInstallTargets, authz.CapLibraryPlugins, authz.CapInstanceView),
		rt("POST /api/plugins/{id}/check", s.handleCheckPlugin, authz.CapLibraryPlugins),
		rt("POST /api/plugins/{id}/download", s.handleDownloadPlugin, authz.CapLibraryPlugins),
		rt("DELETE /api/plugins/{id}/versions", s.handleDeletePluginVersion, authz.CapLibraryPlugins),
		rt("PUT /api/plugins/{id}/policy", s.handlePluginPolicy, authz.CapLibraryPlugins),

		// Seeing what a server has installed is reading its state; changing it
		// decides what code the server loads.
		rt("GET /api/instances/{id}/plugins", s.handleListInstancePlugins, authz.CapInstanceView),
		rt("POST /api/instances/{id}/plugins", s.handleInstallInstancePlugin, authz.CapInstancePlugins),
		rt("PUT /api/instances/{id}/plugins", s.handleToggleInstancePlugin, authz.CapInstancePlugins),
		rt("POST /api/instances/{id}/plugins/adopt", s.handleAdoptInstancePlugin, authz.CapInstancePlugins),
		// The one that writes to the shared library, so it needs the library
		// capability as well as the server's.
		rt("POST /api/instances/{id}/plugins/library", s.handleImportInstancePluginToLibrary, authz.CapInstancePlugins, authz.CapLibraryPlugins),
		rt("POST /api/instances/{id}/plugins/reconcile", s.handleReconcileInstancePlugins, authz.CapInstancePlugins),
		rt("POST /api/instances/{id}/plugins/rollback", s.handleRollbackInstancePlugin, authz.CapInstancePlugins),
		rt("POST /api/instances/{id}/plugins/accept", s.handleAcceptInstancePlugin, authz.CapInstancePlugins),
		rt("DELETE /api/instances/{id}/plugins", s.handleUninstallInstancePlugin, authz.CapInstancePlugins),

		// Schematics. Panel-wide for the same reason plugins are: a build is
		// held once and copied into whichever server wants it. The market is
		// two segments deep, like the plugin config routes, so "market" can
		// never be reachable as the id of a build somebody happened to name
		// that.
		rt("GET /api/schematics", s.handleSchematicLibrary, authz.CapLibrarySchems),
		rt("POST /api/schematics/upload", s.handleUploadSchematics, authz.CapLibrarySchems),
		rt("POST /api/schematics/rescan", s.handleRescanSchematics, authz.CapLibrarySchems),
		rt("GET /api/schematics/market", s.handleBrowseSchematics, authz.CapLibrarySchems),
		rt("POST /api/schematics/market/install", s.handleInstallMarketSchematic, authz.CapLibrarySchems),
		rt("POST /api/schematics/market/sources", s.handleAddSchematicSource, authz.CapLibrarySchems),
		rt("PUT /api/schematics/market/sources/{sourceId}", s.handleUpdateSchematicSource, authz.CapLibrarySchems),
		rt("DELETE /api/schematics/market/sources/{sourceId}", s.handleDeleteSchematicSource, authz.CapLibrarySchems),
		rt("PUT /api/schematics/{id}", s.handleUpdateSchematic, authz.CapLibrarySchems),
		rt("DELETE /api/schematics/{id}", s.handleDeleteSchematic, authz.CapLibrarySchems),
		rt("GET /api/schematics/{id}/preview", s.handleSchematicPreview, authz.CapLibrarySchems),
		rt("GET /api/schematics/{id}/download", s.handleDownloadSchematic, authz.CapLibrarySchems),
		// Writes into a server named in the body rather than in the path, so
		// the instance grant has to be checked inside the handler. See the
		// proposal's step 3.
		rt("POST /api/schematics/{id}/install", s.handleInstallSchematic, authz.CapLibrarySchems, authz.CapInstanceSchematics),
		// The other direction: a build saved in-game with //schem save, taken
		// out of the server it was made on and into the library.
		rt("POST /api/instances/{id}/schematics", s.handleImportInstanceSchematic, authz.CapInstanceSchematics, authz.CapLibrarySchems),

		// Java runtimes. Panel-wide rather than per-instance: one download
		// serves every server that needs that version.
		rt("GET /api/java", s.handleJavaOverview, authz.CapPanelJava),
		rt("GET /api/java/available", s.handleListJavaMajors, authz.CapPanelJava),
		rt("POST /api/java/install", s.handleInstallJava, authz.CapPanelJava),
		rt("POST /api/java/install/cancel", s.handleCancelJavaInstall, authz.CapPanelJava),
		rt("DELETE /api/java/{id}", s.handleDeleteJava, authz.CapPanelJava),

		// Databases. Panel-wide like the Java runtimes and for the same reason
		// — one download of MySQL serves every server that needs one — but with
		// a second layer the runtimes do not have: an engine is the binaries, a
		// service is a data directory and a process. Hence two sets of routes.
		//
		// One capability covers both layers, and it is a dangerous one: the
		// panel has to be able to show the operator a database password, so it
		// stores them in the clear.
		rt("GET /api/databases", s.handleDatabaseOverview, authz.CapPanelDatabases),
		// Two segments deep on purpose, the same way the plugin config routes
		// are: "engines" must never be reachable as a service id.
		rt("GET /api/databases/engines/{engine}/versions", s.handleListDatabaseVersions, authz.CapPanelDatabases),
		rt("POST /api/databases/engines/install", s.handleInstallDatabase, authz.CapPanelDatabases),
		rt("POST /api/databases/engines/install/cancel", s.handleCancelDatabaseInstall, authz.CapPanelDatabases),
		rt("DELETE /api/databases/engines/{id}", s.handleDeleteDatabaseEngine, authz.CapPanelDatabases),

		rt("POST /api/databases/services", s.handleCreateDatabase, authz.CapPanelDatabases),
		rt("PUT /api/databases/services/{id}", s.handleUpdateDatabase, authz.CapPanelDatabases),
		rt("DELETE /api/databases/services/{id}", s.handleDeleteDatabase, authz.CapPanelDatabases),
		rt("POST /api/databases/services/{id}/start", s.handleDatabasePower(true), authz.CapPanelDatabases),
		rt("POST /api/databases/services/{id}/stop", s.handleDatabasePower(false), authz.CapPanelDatabases),
		rt("GET /api/databases/services/{id}/logs", s.handleDatabaseLogs, authz.CapPanelDatabases),

		// The one route that carries two capabilities in two directions rather
		// than as a pair of requirements. The socket streams console output,
		// which is CapInstanceView, and it also accepts {"type":"command"},
		// which is CapInstanceConsole — see the "command" case in ws.go.
		//
		// So it declares only the read half here. Enforcement has to check the
		// write half per message, and a caller without it gets a read-only
		// socket rather than a refused handshake: somebody who may watch the
		// console should see it, just not type into it.
		rt("GET /api/instances/{id}/console", s.handleConsoleSocket, authz.CapInstanceView),

		// File manager. Every path here is confined to the instance directory
		// by os.Root; see internal/serverfiles. That confinement is what makes
		// the read capability safe to hand out — and what the write one gets
		// you past, since a jar written into plugins/ runs as the panel does.
		rt("GET /api/instances/{id}/files", s.handleListFiles, authz.CapInstanceFilesRead),
		rt("DELETE /api/instances/{id}/files", s.handleDeleteFile, authz.CapInstanceFilesWrite),
		rt("GET /api/instances/{id}/files/content", s.handleReadFile, authz.CapInstanceFilesRead),
		rt("PUT /api/instances/{id}/files/content", s.handleWriteFile, authz.CapInstanceFilesWrite),
		rt("GET /api/instances/{id}/files/download", s.handleDownloadFile, authz.CapInstanceFilesRead),
		rt("POST /api/instances/{id}/files/upload", s.handleUploadFile, authz.CapInstanceFilesWrite),
		rt("POST /api/instances/{id}/files/mkdir", s.handleMkdir, authz.CapInstanceFilesWrite),
		rt("POST /api/instances/{id}/files/rename", s.handleRenameFile, authz.CapInstanceFilesWrite),
		rt("GET /api/instances/{id}/files/schematic", s.handleSchematic, authz.CapInstanceFilesRead),

		// Directories on the host, for the instance directory picker. Read-only,
		// and not confined to an instance — see handlers_hostfs.go. Which is
		// why it is its own dangerous capability rather than riding along with
		// the file manager's.
		rt("GET /api/fs", s.handleBrowseHost, authz.CapPanelHostFS),
		rt("GET /api/fs/inspect", s.handleInspectHost, authz.CapPanelHostFS),

		// Resource usage.
		rt("GET /api/instances/{id}/metrics", s.handleInstanceMetrics, authz.CapInstanceView),
		rt("GET /api/system", s.handleSystem, authz.CapPanelSystem),

		// Panel self-update. The mirror and channel settings sit here rather
		// than under the panel settings capability: they only decide where the
		// update comes from, which is meaningless to somebody who cannot apply
		// one.
		rt("GET /api/update", s.handleUpdateStatus, authz.CapPanelUpdate),
		rt("POST /api/update/check", s.handleUpdateCheck, authz.CapPanelUpdate),
		rt("POST /api/update/apply", s.handleUpdateApply, authz.CapPanelUpdate),
		rt("POST /api/update/rollback", s.handleUpdateRollback, authz.CapPanelUpdate),
		rt("PUT /api/update/mirror", s.handleUpdateMirror, authz.CapPanelUpdate),
		rt("PUT /api/update/channel", s.handleUpdateChannel, authz.CapPanelUpdate),

		// Host shell. The routes exist whatever the switch says — the status
		// one is what the settings page renders the switch from, and the other
		// two refuse while it is off — so turning the terminal on takes effect
		// immediately instead of waiting for a panel restart.
		rt("GET /api/terminal", s.handleTerminalStatus, authz.CapPanelTerminal),
		rt("PUT /api/terminal", s.handleTerminalToggle, authz.CapPanelTerminal),
		rt("GET /api/terminal/session", s.handleTerminalSocket, authz.CapPanelTerminal),
	}
}
