package dbruntime

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Everything an engine needs done differently lives here: how a fresh data
// directory is made, how the daemon is launched, and how it is asked to stop.
//
// There is no interface over the three. They agree on so little — one wants a
// separate bootstrap pass, one refuses to be initialised as root, one has no
// concept of a user at all — that an interface would be three implementations
// of a shape that fits none of them, plus the branches anyway.

// bootstrapFile is the SQL MySQL runs on its first start. It stays on disk
// until the server has come up with it applied; see Manager.start.
const bootstrapFile = "bootstrap.sql"

// initialize prepares a service's data directory. It is called once, when the
// service is created, and must leave nothing behind if it fails.
func initialize(ctx context.Context, install Install, service Service) error {
	for _, dir := range []string{service.DataDir(), service.socketDir()} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return err
		}
	}
	// The engine runs as another account from here on, so it has to own what
	// it is about to write into. Done before initialisation rather than after:
	// initdb creates the contents, and it creates them as that account.
	if err := chownTree(service.Dir, service.RunAs); err != nil {
		return err
	}

	var err error
	switch service.Engine {
	case EngineMySQL:
		err = initMySQL(ctx, install, service)
	case EnginePostgreSQL:
		err = initPostgres(ctx, install, service)
	case EngineMongoDB:
		// MongoDB creates everything it needs on first start, and creates
		// databases lazily when something writes to them. Nothing to do.
	default:
		err = fmt.Errorf("%w: %s", ErrUnknownEngine, service.Engine)
	}
	if err != nil {
		return err
	}

	// And again afterwards, for whatever the panel itself wrote during
	// initialisation rather than the engine. MySQL's bootstrap.sql is the one
	// that matters: it is created 0600 by this process, and mysqld — already
	// dropped to the run-as account by the time it reads it — aborts the whole
	// start with a permission error that names a file sitting right there.
	return chownTree(service.Dir, service.RunAs)
}

// startCommand builds the daemon process. Its stdout and stderr are wired up by
// the caller.
func startCommand(install Install, service Service) (*exec.Cmd, error) {
	var cmd *exec.Cmd
	switch service.Engine {
	case EngineMySQL:
		cmd = mysqlStart(install, service)
	case EnginePostgreSQL:
		cmd = postgresStart(install, service)
	case EngineMongoDB:
		cmd = mongoStart(install, service)
	default:
		return nil, fmt.Errorf("%w: %s", ErrUnknownEngine, service.Engine)
	}
	cmd.Dir = service.Dir
	if err := applyCredential(cmd, service.RunAs); err != nil {
		return nil, err
	}
	return cmd, nil
}

// stopCommand is how an engine is asked to shut down cleanly, or nil when
// signalling the process is the way.
//
// It exists for Windows, where there is no signal to send: an engine with its
// own shutdown command can be stopped properly there, and one without cannot.
// On Unix these commands are used anyway — pg_ctl and mysqladmin wait for the
// server to finish flushing, which a bare SIGTERM does not.
func stopCommand(install Install, service Service) *exec.Cmd {
	switch service.Engine {
	case EnginePostgreSQL:
		binary := findBinary(install.Path, "pg_ctl"+exeSuffix())
		if binary == "" {
			return nil
		}
		// "fast" rolls back open transactions and shuts down rather than
		// waiting for clients to disconnect, which is what stopping from a
		// panel button means.
		cmd := exec.Command(binary, "-D", service.DataDir(), "-m", "fast", "-w", "-t", "60", "stop")
		_ = applyCredential(cmd, service.RunAs)
		return cmd
	case EngineMySQL:
		binary := findBinary(install.Path, "mysqladmin"+exeSuffix())
		if binary == "" {
			return nil
		}
		cmd := exec.Command(binary,
			"--no-defaults",
			"--host=127.0.0.1",
			"--port="+strconv.Itoa(service.Port),
			"--protocol=TCP",
			"--user=root",
			"--password="+service.Password,
			"shutdown",
		)
		_ = applyCredential(cmd, service.RunAs)
		return cmd
	}
	// MongoDB's tarball has no admin client, so it gets a signal.
	return nil
}

// ------------------------------------------------------------------- MySQL

func initMySQL(ctx context.Context, install Install, service Service) error {
	args := []string{
		"--no-defaults",
		"--initialize-insecure",
		"--basedir=" + install.Path,
		"--datadir=" + service.DataDir(),
	}
	// mysqld refuses to run as root unless it is told to in so many words.
	// Reached only when the machine has no unprivileged account to hand the
	// engine to; see resolveRunAs.
	if service.RunAs == "" && runtime.GOOS != "windows" && os.Geteuid() == 0 {
		args = append(args, "--user=root")
	}

	initCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(initCtx, install.ServerPath, args...)
	cmd.Dir = service.Dir
	if err := applyCredential(cmd, service.RunAs); err != nil {
		return err
	}
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("初始化 MySQL 数据目录失败：%w\n%s", err, lastLines(string(output), 6))
	}

	// --initialize-insecure leaves root with no password at all, and the client
	// that would normally fix that cannot run before the server is up — and, on
	// a current Ubuntu, cannot run at all, because the bundled mysql client
	// wants libncurses5. So the fix is queued as SQL for every start to run:
	// mysqld applies --init-file before it accepts a connection, so there is no
	// window in which the empty password is reachable over the network.
	//
	// The file stays for the life of the service rather than being deleted once
	// it has been applied. Deleting it needs a reliable "the init file has run"
	// signal, and there is none — mysqld opens its listening socket *before* it
	// reads the file, so the obvious signal fires while the file is still about
	// to be read, and removing it there aborts the very start that looked
	// successful. Every statement in it is idempotent, so replaying it costs a
	// few writes to mysql.user; it holds the same password databases.json
	// already holds, in a 0600 file inside a directory only this service's
	// account can enter.
	return os.WriteFile(filepath.Join(service.Dir, bootstrapFile), []byte(mysqlBootstrap(service)), 0o600)
}

// mysqlBootstrap is the SQL that turns an insecure fresh install into the
// service the operator asked for. Every statement is idempotent, because a
// crash between "server came up" and "bootstrap file deleted" means it runs
// again on the next start.
//
// root gets the same password as the application user rather than a second one
// nobody would ever be shown: the panel needs a working root login to shut the
// server down cleanly (see stopCommand), and a hidden credential the operator
// cannot see is worse than a shared one on a loopback database.
func mysqlBootstrap(service Service) string {
	var sql strings.Builder
	fmt.Fprintf(&sql, "ALTER USER 'root'@'localhost' IDENTIFIED BY '%s';\n", service.Password)
	fmt.Fprintf(&sql, "CREATE DATABASE IF NOT EXISTS `%s` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;\n",
		service.Database)
	if service.User != "" && service.User != "root" {
		// '%' rather than 'localhost': a server on another machine is exactly
		// the case a non-loopback bind exists for, and a user that can only log
		// in locally would make that setting do nothing.
		fmt.Fprintf(&sql, "CREATE USER IF NOT EXISTS '%s'@'%%' IDENTIFIED BY '%s';\n",
			service.User, service.Password)
		fmt.Fprintf(&sql, "ALTER USER '%s'@'%%' IDENTIFIED BY '%s';\n", service.User, service.Password)
		fmt.Fprintf(&sql, "GRANT ALL PRIVILEGES ON `%s`.* TO '%s'@'%%';\n", service.Database, service.User)
	}
	sql.WriteString("FLUSH PRIVILEGES;\n")
	return sql.String()
}

func mysqlStart(install Install, service Service) *exec.Cmd {
	args := []string{
		"--no-defaults",
		"--basedir=" + install.Path,
		"--datadir=" + service.DataDir(),
		"--port=" + strconv.Itoa(service.Port),
		"--bind-address=" + service.Bind,
		"--pid-file=" + filepath.Join(service.socketDir(), "mysqld.pid"),
		// The X protocol is a second listener on a second port nobody asked
		// for, and it is the commonest reason a second MySQL service on one
		// machine fails to start.
		"--mysqlx=0",
	}
	if runtime.GOOS != "windows" {
		// mysqld has no way to run without a Unix socket, so unlike PostgreSQL
		// a path too long for sockaddr_un has to go somewhere rather than away.
		// The system temp directory keyed by service id is short, unique per
		// service, and a stale file there is simply overwritten on the next
		// start.
		socket := filepath.Join(service.socketDir(), "mysql.sock")
		if _, ok := unixSocketDir(service.socketDir(), "mysql.sock"); !ok {
			socket = filepath.Join(os.TempDir(), "hypercraft-"+service.ID+".sock")
		}
		args = append(args, "--socket="+socket)
		if service.RunAs == "" && os.Geteuid() == 0 {
			args = append(args, "--user=root")
		}
	}
	if bootstrap := filepath.Join(service.Dir, bootstrapFile); fileExists(bootstrap) {
		args = append(args, "--init-file="+bootstrap)
	}
	return exec.Command(install.ServerPath, args...)
}

// --------------------------------------------------------------- PostgreSQL

func initPostgres(ctx context.Context, install Install, service Service) error {
	initdb := findBinary(install.Path, "initdb"+exeSuffix())
	if initdb == "" {
		return fmt.Errorf("%w: 这个 PostgreSQL 安装里没有 initdb", ErrNotFound)
	}

	// initdb reads the password from a file rather than a command line, which
	// is the whole reason to use one: an argument would be visible in ps to
	// every account on the machine.
	pwfile := filepath.Join(service.Dir, ".initpw")
	if err := os.WriteFile(pwfile, []byte(service.Password+"\n"), 0o600); err != nil {
		return err
	}
	defer os.Remove(pwfile)
	if err := chownTree(pwfile, service.RunAs); err != nil {
		return err
	}

	initCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(initCtx, initdb,
		"-D", service.DataDir(),
		"-U", service.User,
		"--pwfile="+pwfile,
		"-A", "scram-sha-256",
		"-E", "UTF8",
		// The C locale is the one collation guaranteed to exist in these
		// builds; asking for the system's would fail on a container with no
		// locales installed at all.
		"--locale=C",
	)
	cmd.Dir = service.Dir
	if err := applyCredential(cmd, service.RunAs); err != nil {
		return err
	}
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("初始化 PostgreSQL 数据目录失败：%w\n%s", err, lastLines(string(output), 6))
	}

	if err := allowPostgresHosts(service); err != nil {
		return err
	}
	return createPostgresDatabase(initCtx, install, service)
}

// allowPostgresHosts opens pg_hba.conf to the network when the service is not
// on loopback. initdb writes rules for 127.0.0.1 and ::1 only, so without this
// a service bound to 0.0.0.0 listens and then refuses everything that arrives.
func allowPostgresHosts(service Service) error {
	if isLoopback(service.Bind) {
		return nil
	}
	path := filepath.Join(service.DataDir(), "pg_hba.conf")
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()

	_, err = file.WriteString("\n# HyperCraft: 这个服务监听在 " + service.Bind + "，放行远程连接（仍然要密码）\n" +
		"host    all             all             0.0.0.0/0               scram-sha-256\n" +
		"host    all             all             ::/0                    scram-sha-256\n")
	return err
}

// createPostgresDatabase makes the application database.
//
// initdb creates a cluster, not a database, and these builds ship no psql or
// createdb to make one with — the whole client suite is left out. The way in
// is the standalone backend: `postgres --single` reads SQL from stdin with no
// server, no port and no authentication, which is what PostgreSQL's own
// bootstrap uses.
func createPostgresDatabase(ctx context.Context, install Install, service Service) error {
	cmd := exec.CommandContext(ctx, install.ServerPath, "--single", "-D", service.DataDir(), "postgres")
	cmd.Dir = service.Dir
	cmd.Stdin = strings.NewReader(
		fmt.Sprintf("CREATE DATABASE %s OWNER %s ENCODING 'UTF8';\n", service.Database, service.User))
	if err := applyCredential(cmd, service.RunAs); err != nil {
		return err
	}
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("创建数据库 %s 失败：%w\n%s", service.Database, err, lastLines(string(output), 6))
	}
	return nil
}

func postgresStart(install Install, service Service) *exec.Cmd {
	args := []string{
		"-D", service.DataDir(),
		"-p", strconv.Itoa(service.Port),
		"-c", "listen_addresses=" + service.Bind,
	}
	// PostgreSQL always creates a Unix socket, and its compiled-in default is a
	// directory an unprivileged account cannot write to. Keeping it inside the
	// service directory is normally the difference between starting and not.
	//
	// Normally: a Unix socket path is capped at around 107 bytes by the kernel,
	// which a data directory a few levels down someone's home directory can
	// exceed on its own. When it will not fit, the socket is switched off
	// rather than moved somewhere shorter and less predictable — nothing the
	// panel does goes through it. The connection string it hands out is TCP,
	// and pg_ctl stops the server by reading postmaster.pid and signalling it,
	// not by connecting. The only thing lost is an operator's own `psql -h
	// /path/to/run`, which still works over TCP with the password on the page.
	if dir, ok := unixSocketDir(service.socketDir(), ".s.PGSQL.65535"); ok {
		args = append(args, "-k", dir)
	} else {
		args = append(args, "-c", "unix_socket_directories=")
	}
	return exec.Command(install.ServerPath, args...)
}

// maxUnixSocketPath is what a sockaddr_un holds, minus room to be wrong about
// it. The real cap is 108 bytes on Linux and 104 on macOS, both including the
// terminator, and a server that starts on one and not the other is worse than
// one that behaves the same everywhere.
const maxUnixSocketPath = 100

// unixSocketDir reports whether a socket named longest can live in dir.
func unixSocketDir(dir, longest string) (string, bool) {
	return dir, len(filepath.Join(dir, longest)) < maxUnixSocketPath
}

// ----------------------------------------------------------------- MongoDB

func mongoStart(install Install, service Service) *exec.Cmd {
	// No --logpath and no --fork: with neither, mongod stays in the foreground
	// and writes to stdout, which is what the panel captures for the other two
	// engines as well.
	return exec.Command(install.ServerPath,
		"--dbpath", service.DataDir(),
		"--port", strconv.Itoa(service.Port),
		"--bind_ip", service.Bind,
	)
}

// ------------------------------------------------------------------ helpers

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// lastLines trims engine output down to the part that says what went wrong.
// initdb in particular narrates its whole run before failing on the last line.
func lastLines(text string, n int) string {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
