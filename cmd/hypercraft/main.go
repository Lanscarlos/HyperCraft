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
	"github.com/lanscarlos/hypercraft/internal/instance"
	"github.com/lanscarlos/hypercraft/internal/javaruntime"
	"github.com/lanscarlos/hypercraft/internal/metrics"
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
		listen        = flag.String("listen", envOr("HYPERCRAFT_LISTEN", ""), "address to bind, e.g. 127.0.0.1:8080 (overrides the stored setting)")
		username      = flag.String("username", envOr("HYPERCRAFT_USERNAME", "admin"), "operator username, used when creating the first credential")
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
	userAgent := "HyperCraft/" + version + " (+https://github.com/Lanscarlos/HyperCraft)"
	downloads := serverjar.NewDownloader(serverjar.NewClient("", userAgent), logger)
	defer downloads.Close()

	// Java runtimes live beside the servers, in the data directory, so a panel
	// that manages its own JDKs stays as movable as one that does not.
	javaInstaller := javaruntime.NewInstaller(
		javaruntime.NewClient("", userAgent),
		javaruntime.NewStore(paths.JavaRoot()),
		logger,
	)
	defer javaInstaller.Close()

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
		// Recorded before the swap so the servers this update is about to stop
		// come back on the other side, whether or not they auto-start.
		BeforeInstall:  func() error { return st.SaveResume(manager.RunningIDs()) },
		InstallAborted: func() { _, _ = st.TakeResume() },
		TriggerRestart: func(binary string) {
			newBinary.Store(&binary)
			cancel()
		},
	}, logger)

	server := api.NewServer(api.Options{
		Manager:  manager,
		Store:    st,
		Sessions: sessions,
		Metrics:  collector,
		Jars:     downloads,
		Java:     javaInstaller,
		Updater:  updater,
		Panel:    panel,
		Version:  version,
		Logger:   logger,
	})

	httpServer := &http.Server{
		Addr:              panel.Listen,
		Handler:           server.Handler(),
		ReadHeaderTimeout: 15 * time.Second,
		// No WriteTimeout: the console websocket is a long-lived response and
		// a write deadline would sever it. Per-frame deadlines guard it instead.
		IdleTimeout: 120 * time.Second,
	}

	go gcSessions(ctx, sessions)
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
	manager.StartAutoStart(resume)

	serverErr := make(chan error, 1)
	go func() {
		logger.Info("listening", "addr", panel.Listen, "url", "http://"+panel.Listen)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
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

	logger.Info("stopping managed servers", "grace", shutdownGrace)
	manager.Shutdown(shutdownGrace)

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

func gcSessions(ctx context.Context, sessions *auth.SessionStore) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sessions.GC()
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
