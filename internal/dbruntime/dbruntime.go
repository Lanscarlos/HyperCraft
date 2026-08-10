// Package dbruntime sets up databases beside the servers the panel runs: it
// downloads an engine's official binaries, initialises a data directory, and
// owns the daemon process from then on.
//
// It exists for the same reason internal/javaruntime does. Half the plugins a
// server ends up running — LuckPerms, CoreProtect, AuthMe, Plan — want a
// database, and "install MySQL" is a package manager, a service unit, a root
// password and a grant statement before the operator gets back to the thing
// they were actually doing. None of that is Minecraft knowledge, and none of it
// should be a prerequisite for ticking a box in a plugin config.
//
// What it deliberately is not: a database administration tool. There is no
// query console, no schema browser and no backup scheduler. The panel gets an
// engine running, hands over a connection string, and stays out of the way.
package dbruntime

import "errors"

var (
	// ErrNotFound is returned for an unknown engine install or service.
	ErrNotFound = errors.New("database not found")
	// ErrInvalidID is returned for an id that does not name a directory in the
	// database root.
	ErrInvalidID = errors.New("invalid database id")
	// ErrUnknownEngine is returned for an engine the panel does not support.
	ErrUnknownEngine = errors.New("unknown database engine")
	// ErrUnsupported is returned for a platform an engine has no build for.
	ErrUnsupported = errors.New("unsupported platform")
	// ErrUnknownRelease is returned when no build matches the request.
	ErrUnknownRelease = errors.New("no matching database build")
	// ErrUpstream wraps anything the download source did that we cannot act on.
	ErrUpstream = errors.New("database download")
	// ErrBusy is returned while another install is already running.
	ErrBusy = errors.New("a database install is already running")
	// ErrExists is returned when that build is already on disk.
	ErrExists = errors.New("this database version is already installed")
	// ErrCancelled is recorded on an install the operator stopped.
	ErrCancelled = errors.New("install cancelled")
	// ErrChecksum is recorded when the archive is not what upstream published.
	ErrChecksum = errors.New("checksum mismatch")
	// ErrInvalidConfig is returned for a service the panel cannot create.
	ErrInvalidConfig = errors.New("invalid database config")
	// ErrAlreadyRunning is returned when a service is already up.
	ErrAlreadyRunning = errors.New("the database is already running")
	// ErrNotRunning is returned when a service is not up.
	ErrNotRunning = errors.New("the database is not running")
	// ErrInUse is returned when something still points at what is being deleted.
	ErrInUse = errors.New("still in use")
)

// Engine ids. They are part of the API and of every directory name on disk, so
// they are lowercase and never change.
const (
	EngineMySQL      = "mysql"
	EnginePostgreSQL = "postgresql"
	EngineMongoDB    = "mongodb"
)

// Engine describes one database the panel can set up.
type Engine struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Note string `json:"note"`
	// Vendor names who publishes the binaries the panel downloads. It is on the
	// page rather than in a comment because two of the three are not the
	// project's own build — see the resolvers.
	Vendor      string `json:"vendor"`
	DefaultPort int    `json:"defaultPort"`
	// AdminUser is the account the panel creates while initialising.
	AdminUser string `json:"adminUser"`
	// Password reports whether the panel can set a password on that account.
	//
	// It cannot for MongoDB: creating a user needs mongosh, which stopped
	// shipping inside the server tarball at 6.0, and the panel is not going to
	// speak the wire protocol to make up the difference. Such a service is
	// bound to the loopback interface and left open, which is the same posture
	// a local Redis or H2 would have and is safe as long as it stays there —
	// see Service.Bind.
	Password bool `json:"password"`
	// Scheme is what a JDBC or driver URL starts with, for the connection
	// string the page hands over.
	Scheme string `json:"scheme"`
	// JDBC is the driver URL prefix a Bukkit plugin config asks for. Empty when
	// the engine has no JDBC driver worth naming — plugins reach MongoDB
	// through its own driver.
	JDBC string `json:"jdbc,omitempty"`
}

// engines is the supported set, in the order the page offers them: the two an
// operator is most likely to have been told to install by a plugin's README
// first.
var engines = []Engine{
	{
		ID:          EngineMySQL,
		Name:        "MySQL",
		Note:        "插件文档里默认的那个，兼容 MariaDB 语法，绝大多数插件优先支持",
		Vendor:      "Oracle 官方构建",
		DefaultPort: 3306,
		AdminUser:   "root",
		Password:    true,
		Scheme:      "mysql",
		JDBC:        "jdbc:mysql://",
	},
	{
		ID:          EnginePostgreSQL,
		Name:        "PostgreSQL",
		Note:        "更严格也更耐造，插件支持面比 MySQL 窄一些，但支持的都很稳",
		Vendor:      "zonky.io 便携构建",
		DefaultPort: 5432,
		AdminUser:   "hypercraft",
		Password:    true,
		Scheme:      "postgresql",
		JDBC:        "jdbc:postgresql://",
	},
	{
		ID:          EngineMongoDB,
		Name:        "MongoDB",
		Note:        "文档型，少数插件（如 Plan、部分跨服同步）用它，不需要建表",
		Vendor:      "MongoDB 官方社区版",
		DefaultPort: 27017,
		AdminUser:   "",
		Password:    false,
		Scheme:      "mongodb",
	},
}

// Engines lists what the panel can install.
func Engines() []Engine {
	out := make([]Engine, len(engines))
	copy(out, engines)
	return out
}

// EngineByID looks one up.
func EngineByID(id string) (Engine, error) {
	for _, engine := range engines {
		if engine.ID == id {
			return engine, nil
		}
	}
	return Engine{}, ErrUnknownEngine
}
