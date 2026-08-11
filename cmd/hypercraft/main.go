// Command hypercraft runs the Minecraft server panel.
//
// The panel is a long-lived daemon that owns the Minecraft server processes.
// The web UI is only a client of it: closing the browser, or restarting your
// laptop's browser session, has no effect on a running server. Stopping the
// panel itself does stop the servers, gracefully — so run it under systemd (or
// any supervisor) rather than in a terminal you intend to close.
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/lanscarlos/hypercraft/internal/api"
	"github.com/lanscarlos/hypercraft/internal/auth"
	"github.com/lanscarlos/hypercraft/internal/config"
	"github.com/lanscarlos/hypercraft/internal/confighist"
	"github.com/lanscarlos/hypercraft/internal/dbruntime"
	"github.com/lanscarlos/hypercraft/internal/hostterm"
	"github.com/lanscarlos/hypercraft/internal/instance"
	"github.com/lanscarlos/hypercraft/internal/javaruntime"
	"github.com/lanscarlos/hypercraft/internal/metrics"
	"github.com/lanscarlos/hypercraft/internal/plugin"
	"github.com/lanscarlos/hypercraft/internal/selfupdate"
	"github.com/lanscarlos/hypercraft/internal/serverjar"
	"github.com/lanscarlos/hypercraft/internal/store"
)

// version is overridden at build time with -ldflags "-X main.version=...".
var version = "dev"

// updateRepo is where the panel looks for new releases. Updates are only ever
// fetched from this repository.
const updateRepo = "Lanscarlos/HyperCraft"

// shutdownGrace is how long managed servers get to save and exit when the
// panel is stopping. Minecraft can take a while to flush a large world.
const shutdownGrace = 2 * time.Minute

// Resource sampling cadence and how much history is kept in memory. One hour
// at 5s is ~720 samples per instance, a few tens of kilobytes.
const (
	metricsInterval = 5 * time.Second
	metricsWindow   = time.Hour
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "hypercraft: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		dataDir       = flag.String("data", envOr("HYPERCRAFT_DATA", "./data"), "directory for panel state and server files")
		listen        = flag.String("listen", envOr("HYPERCRAFT_LISTEN", ""), "address to bind, e.g. 0.0.0.0:19190 (overrides the stored setting)")
		username      = flag.String("username", envOr("HYPERCRAFT_USERNAME", "admin"), "operator username, used when creating the first credential")
		tlsCert       = flag.String("tls-cert", envOr("HYPERCRAFT_TLS_CERT", ""), "PEM certificate chain; serves HTTPS when given with -tls-key")
		tlsKey        = flag.String("tls-key", envOr("HYPERCRAFT_TLS_KEY", ""), "PEM private key for -tls-cert")
		resetPassword = flag.Bool("reset-password", false, "generate a new random password, print it, and exit")
		logLevel      = flag.String("log-level", envOr("HYPERCRAFT_LOG_LEVEL", "info"), "debug, info, warn or error")
		showVersion   = flag.Bool("version", false, "print the version and exit")
	)
	flag.Parse()

	if *showVersion {
		fmt.Println("hypercraft", version)
		return nil
	}

	logger := newLogger(*logLevel)

	root, err := filepath.Abs(*dataDir)
	if err != nil {
		return fmt.Errorf("resolve data dir: %w", err)
	}
	paths := config.NewPaths(root)

	st, err := store.New(paths)
	if err != nil {
		return err
	}
	panel, err := st.LoadPanel()
	if err != nil {
		return err
	}
	if *listen != "" {
		panel.Listen = *listen
	}

	if *resetPassword {
		return resetOperatorPassword(st, panel, *username)
	}

	// First run: nobody can reach the panel until a credential exists, so mint
	// one and print it. This is better than a well-known default password.
	if panel.Credential.IsZero() {
		password, err := auth.GeneratePassword()
		if err != nil {
			return err
		}
		cred, err := auth.NewCredential(*username, password)
		if err != nil {
			return err
		}
		panel.Credential = cred
		if err := st.SavePanel(panel); err != nil {
			return err
		}
		printCredentialBanner(cred.Username, password)
	}
	if err := st.SavePanel(panel); err != nil {
		return err
	}

	sessions := auth.NewSessionStore(time.Duration(panel.SessionTTLHours) * time.Hour)

	manager := instance.NewManager(st, paths.ServersRoot(), logger)
	configs, err := st.LoadInstances()
	if err != nil {
		return err
	}
	manager.Load(configs)
	logger.Info("panel starting",
		"version", version,
		"data", root,
		"instances", len(configs),
		"listen", panel.Listen,
	)

	collector := metrics.New(metricsInterval, metricsWindow, root, logger)

	// Core downloads run in the daemon, so they keep going with nobody watching
	// — same reason the server processes live here rather than in a request.
	// They land in a panel-wide library beside the Java runtimes: downloaded
	// once, copied into as many instances as the operator makes.
	userAgent := "HyperCraft/" + version + " (+https://github.com/Lanscarlos/HyperCraft)"
	downloads := serverjar.NewDownloader(
		serverjar.NewClient("", userAgent),
		serverjar.NewLibrary(paths.CoresRoot()),
		logger,
	)
	defer downloads.Close()

	// Java runtimes live beside the servers, in the data directory, so a panel
	// that manages its own JDKs stays as movable as one that does not.
	javaInstaller := javaruntime.NewInstaller(
		javaruntime.NewClient("", userAgent),
		javaruntime.NewStore(paths.JavaRoot()),
		logger,
	)
	defer javaInstaller.Close()

	// Databases sit beside the Java runtimes for the same reason: half the
	// plugins a server runs want one, and installing MySQL by hand is a
	// package manager and a service unit before the operator gets back to
	// Minecraft. The engines are shared; each database is its own data
	// directory and its own process, owned by this daemon like the servers are.
	databaseInstaller := dbruntime.NewInstaller(
		dbruntime.NewClient(userAgent),
		dbruntime.NewStore(paths.DatabaseEnginesRoot()),
		logger,
	)
	defer databaseInstaller.Close()

	databaseConfigs, err := st.LoadDatabases()
	if err != nil {
		return fmt.Errorf("load databases: %w", err)
	}
	databases, err := dbruntime.NewManager(
		paths.DatabaseRoot(), databaseInstaller.Store(), st, databaseConfigs, logger)
	if err != nil {
		return fmt.Errorf("prepare databases: %w", err)
	}

	// Plugins are a panel-wide library too, and for a stronger reason than
	// cores: a plugin has a version history that instances pin, so the panel
	// keeps every release it downloaded and hands out copies. Downloads have
	// their own mirror setting — see config.Panel.PluginMirror.
	pluginLibrary := plugin.NewLibrary(paths.PluginsRoot())
	// A library written by an older build may hold one release several times
	// over — once per platform — because that is how the panel used to read
	// Hangar and Modrinth. Folded back before anything reads the library, so
	// no page ever renders the split. A no-op on every boot after the first.
	pluginLibrary.Regroup(logger)
	pluginClient := plugin.NewClient("", userAgent)
	pluginClient.SetMirror(panel.PluginMirror)
	// A token is what makes the operator's own private repository readable, and
	// it also lifts the anonymous API's 60 calls an hour out of the way. There
	// can be several — one per account whose repositories the panel reads — and
	// a plugin source names which of them it is read with.
	pluginClient.SetTokens(api.PluginTokens(panel.GitHubTokens))
	// 插件市场 opens on a curated shelf that has to be read from the registries
	// before it can be drawn, which is a couple of seconds nobody should spend
	// looking at an empty page. Read once here, in the background, so it is
	// already there when somebody opens the market; refreshed from then on
	// behind whoever is looking at it. See plugin/picks.go.
	pluginClient.Registry().RefreshPicks()
	pluginDownloads := plugin.NewDownloader(pluginClient, pluginLibrary, logger)
	defer pluginDownloads.Close()
	instancePlugins := plugin.NewInstances(pluginLibrary, paths.InstancePluginsFile())
	// The merge above rewrote the library's tags; the servers' records still
	// name the old ones. Re-pointed by digest, which is the one thing about a
	// jar that never changed.
	instancePlugins.Realign(pluginLibrary, logger)
	// Every plugin change lands in a directory the running server read once and
	// will not read again, so the panel records what each server has yet to see.
	pendingPlugins := plugin.NewPending(paths.PendingPluginsFile())

	// The config history. One Git repository per instance, in the panel's data
	// directory rather than in the server's — see internal/confighist. Wiring
	// it up is what installs the lifecycle snapshots, so it has to happen
	// before anything can start a server.
	configHistory := confighist.New(paths.ConfigHistoryRoot(), paths.ConfigHistoryFile(), logger)
	wireConfigHistory(configHistory, manager, instancePlugins, logger)

	ctx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	// A second, manually cancellable layer: an in-panel update shuts the daemon
	// down the same way a SIGTERM does, then execs the new binary.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Set to the newly installed binary when the shutdown below should hand
	// over to it instead of exiting. Read only after every server has stopped.
	// The path comes from the updater rather than os.Executable: by then the
	// running image has been renamed aside and the OS would report the backup.
	var newBinary atomic.Pointer[string]

	updater := selfupdate.NewService(updateRepo, version, panel.Mirror(), selfupdate.ParseChannel(panel.Channel()), selfupdate.Hooks{
		// The first half of an update, running beside the download: empty the
		// machine so the swap at the end has no live child processes under it.
		// The list is recorded before anything is stopped, so the servers come
		// back on the other side whether or not they auto-start.
		StopServers: func(_ context.Context, report func(selfupdate.Shutdown)) error {
			if err := st.SaveResume(manager.RunningIDs()); err != nil {
				return err
			}
			manager.StopAll(shutdownGrace, func(progress instance.Stopping) {
				report(selfupdate.Shutdown{
					Total:   progress.Total,
					Stopped: progress.Stopped,
					Pending: progress.Pending,
				})
			})
			return nil
		},
		// The update is not happening, so the servers it stopped go back up and
		// the resume list — which describes a restart that will not come — is
		// consumed here rather than left for the next real one.
		ServersAborted: func() {
			resume, err := st.TakeResume()
			if err != nil {
				logger.Warn("could not read the resume list after an abandoned update", "err", err)
				return
			}
			if len(resume) == 0 {
				return
			}
			logger.Info("update abandoned, starting the servers it stopped", "count", len(resume))
			manager.StartEach(resume)
		},
		TriggerRestart: func(binary string) {
			newBinary.Store(&binary)
			cancel()
		},
	}, logger)

	// The shell the terminal page hands out. Constructed unconditionally so the
	// settings page can describe what enabling it would give you; it starts no
	// process until the operator turns the feature on and opens a terminal.
	shells := hostterm.New(hostterm.Options{
		Shell:  panel.Terminal.Shell,
		Dir:    root,
		Logger: logger,
	})
	if panel.Terminal.Enabled {
		logger.Warn("host terminal is enabled",
			"shell", shells.Shell(),
			"note", "anyone who can sign in to the panel gets a shell as this user",
		)
	}

	server := api.NewServer(api.Options{
		Manager:  manager,
		Store:    st,
		Sessions: sessions,
		Metrics:  collector,
		Paths:    paths,
		Jars:     downloads,
		Java:     javaInstaller,
		Updater:  updater,
		Terminal: shells,
		Panel:    panel,
		Version:  version,
		Logger:   logger,

		Plugins:         pluginDownloads,
		InstancePlugins: instancePlugins,
		PendingPlugins:  pendingPlugins,
		ConfigHistory:   configHistory,

		DatabaseInstalls: databaseInstaller,
		Databases:        databases,
	})

	// The panel can terminate TLS itself when handed a certificate. That does
	// not replace a reverse proxy — one is still the better answer when you
	// have a domain and want automatic renewal — but it covers the cases a
	// proxy does not: a certificate the operator already holds, an internal CA,
	// or a machine that is only ever reached by IP and so can never be issued
	// a public certificate at all.
	tlsConfig, err := loadTLS(*tlsCert, *tlsKey)
	if err != nil {
		return err
	}

	httpServer := &http.Server{
		Addr:              panel.Listen,
		Handler:           server.Handler(),
		TLSConfig:         tlsConfig,
		ReadHeaderTimeout: 15 * time.Second,
		// No WriteTimeout: the console websocket is a long-lived response and
		// a write deadline would sever it. Per-frame deadlines guard it instead.
		IdleTimeout: 120 * time.Second,
	}

	go gcSessions(ctx, sessions, server, logger)
	go updater.Run(ctx)
	go collector.Run(ctx, func() []metrics.Target {
		instances := manager.List()
		targets := make([]metrics.Target, 0, len(instances))
		for _, inst := range instances {
			status := inst.Status()
			targets = append(targets, metrics.Target{ID: status.ID, PID: status.PID})
		}
		return targets
	})

	// Servers stopped by an update restart are listed here; the file is
	// consumed on read, so a failed start is not retried on every boot.
	resume, err := st.TakeResume()
	if err != nil {
		logger.Warn("could not read the resume list", "err", err)
	}
	if len(resume) > 0 {
		logger.Info("resuming servers stopped by an update", "count", len(resume))
	}
	// Databases first, and synchronously: a server set to start on boot is
	// likely to be the reason a database is set to start on boot, and one that
	// comes up to find its database still starting logs a connection failure
	// and, for several plugins, disables itself for the session.
	databases.StartAuto()
	manager.StartAutoStart(resume)

	serverErr := make(chan error, 1)
	go func() {
		scheme := "http"
		if tlsConfig != nil {
			scheme = "https"
		}
		logger.Info("listening", "addr", panel.Listen, "url", scheme+"://"+panel.Listen, "tls", tlsConfig != nil)

		// The certificate is already parsed into TLSConfig, so the paths are
		// passed empty here rather than read a second time.
		serve := httpServer.ListenAndServe
		if tlsConfig != nil {
			serve = func() error { return httpServer.ListenAndServeTLS("", "") }
		}
		if err := serve(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
			return
		}
		serverErr <- nil
	}()

	select {
	case err := <-serverErr:
		if err != nil {
			return fmt.Errorf("http server: %w", err)
		}
	case <-ctx.Done():
		logger.Info("shutdown signal received")
	}

	// Close the HTTP surface first so nobody can start a new server while the
	// existing ones are being stopped.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Warn("http shutdown was not clean", "err", err)
	}
	// Downloads go before the servers do: a half-written jar is worth nothing,
	// and the servers deserve the whole shutdown budget.
	downloads.Close()
	javaInstaller.Close()
	databaseInstaller.Close()
	pluginDownloads.Close()

	logger.Info("stopping managed servers", "grace", shutdownGrace)
	manager.Shutdown(shutdownGrace)

	// Databases go last, after the servers that were connected to them: a
	// server shutting down flushes to its database, and pulling the database
	// first turns a clean stop into a stack trace in the server log.
	databases.Close()

	// Only now, with no child processes left, is it safe to replace this
	// process image: exec keeps the PID but inherits nothing else.
	if binary := newBinary.Load(); binary != nil {
		logger.Info("restarting into the updated binary", "path", *binary)
		if err := selfupdate.Restart(*binary); err != nil {
			return fmt.Errorf("restart after update: %w", err)
		}
		// Reached on Windows only, where the replacement is a new process that
		// is already running and this one just exits.
		return nil
	}

	logger.Info("bye")
	return nil
}

// loadTLS builds the server's TLS configuration, or returns nil to keep
// serving plain HTTP.
//
// The certificate is parsed here rather than left to ListenAndServeTLS so a
// wrong path fails immediately, naming the file, instead of after the panel has
// claimed the port and started the servers.
func loadTLS(certFile, keyFile string) (*tls.Config, error) {
	switch {
	case certFile == "" && keyFile == "":
		return nil, nil
	case certFile == "" || keyFile == "":
		return nil, errors.New("-tls-cert and -tls-key have to be given together")
	}

	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("load TLS certificate: %w", err)
	}
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		// TLS 1.2 is the floor. Everything that could reach this panel speaks
		// it, and the versions below it have been broken for years.
		MinVersion: tls.VersionTLS12,
	}, nil
}

func resetOperatorPassword(st *store.Store, panel config.Panel, username string) error {
	if panel.Credential.Username != "" {
		username = panel.Credential.Username
	}
	password, err := auth.GeneratePassword()
	if err != nil {
		return err
	}
	cred, err := auth.NewCredential(username, password)
	if err != nil {
		return err
	}
	panel.Credential = cred
	if err := st.SavePanel(panel); err != nil {
		return err
	}
	printCredentialBanner(username, password)
	return nil
}

func printCredentialBanner(username, password string) {
	line := strings.Repeat("=", 58)
	fmt.Printf(`
%s
  HyperCraft 面板登录凭据（仅显示这一次，请立即保存）

    用户名: %s
    密码:   %s

  忘记密码可运行:  hypercraft -reset-password
%s

`, line, username, password, line)
}

// gcSessions drops expired sessions and writes out device activity on the same
// slow timer. Neither is urgent enough to deserve its own goroutine, and both
// are cheap enough that an hour is a generous interval.
func gcSessions(ctx context.Context, sessions *auth.SessionStore, server *api.Server, log *slog.Logger) {
	flush := func() {
		if err := server.FlushDevices(); err != nil {
			log.Warn("could not persist device activity", "err", err)
		}
	}

	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			// One last write, so a clean shutdown does not discard up to an
			// hour of "last used" the ticker has not got to yet.
			flush()
			return
		case <-ticker.C:
			sessions.GC()
			server.SweepRateLimits()
			flush()
		}
	}
}

func newLogger(level string) *slog.Logger {
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(level)); err != nil {
		lvl = slog.LevelInfo
	}
	return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: lvl}))
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
