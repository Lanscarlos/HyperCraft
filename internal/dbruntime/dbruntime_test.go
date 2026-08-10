package dbruntime

import (
	"errors"
	"strings"
	"testing"
)

func TestEngineByID(t *testing.T) {
	for _, id := range []string{EngineMySQL, EnginePostgreSQL, EngineMongoDB} {
		engine, err := EngineByID(id)
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		if engine.DefaultPort == 0 || engine.Name == "" || engine.Scheme == "" {
			t.Errorf("%s is not fully described: %+v", id, engine)
		}
	}
	if _, err := EngineByID("mariadb"); !errors.Is(err, ErrUnknownEngine) {
		t.Errorf("got %v, want ErrUnknownEngine", err)
	}
}

// A version reaches Resolve from an operator as well as from a manifest, and it
// is pasted straight into a download URL, so anything that could steer that URL
// somewhere else has to be refused.
func TestValidVersion(t *testing.T) {
	for _, ok := range []string{"8.0.45", "17.6", "8", "8.4.10"} {
		if !validVersion(ok) {
			t.Errorf("%q should be accepted", ok)
		}
	}
	for _, bad := range []string{
		"", "..", "8..0", ".8", "8.", "8.0.45/../../etc", "8.0.45?x=1",
		"latest", "8.0.45 ", "../8.0", "8.0.45%2f", strings.Repeat("1", 40),
	} {
		if validVersion(bad) {
			t.Errorf("%q should be refused", bad)
		}
	}
}

func TestServiceValidate(t *testing.T) {
	base := func() Service {
		return Service{
			ID: "mysql", Name: "生存服数据库", Engine: EngineMySQL,
			Database: "survival", User: "hypercraft", Password: "s3cr3t-password",
			Port: 3306, Bind: "127.0.0.1",
		}
	}

	normal := base()
	if err := normal.validate(); err != nil {
		t.Fatalf("a normal service should validate: %v", err)
	}

	cases := map[string]func(*Service){
		"empty database":     func(s *Service) { s.Database = "" },
		"database with dash": func(s *Service) { s.Database = "my-db" },
		"database with tick": func(s *Service) { s.Database = "a`b" },
		"leading digit":      func(s *Service) { s.Database = "1db" },
		"user with quote":    func(s *Service) { s.User = "a'b" },
		"password too short": func(s *Service) { s.Password = "short" },
		"password quote":     func(s *Service) { s.Password = "abcdefg'h" },
		"password backslash": func(s *Service) { s.Password = `abcdefg\h` },
		"password space":     func(s *Service) { s.Password = "abcd efgh" },
		"bad port":           func(s *Service) { s.Port = 0 },
		"bad bind":           func(s *Service) { s.Bind = "example.com" },
		"bad id":             func(s *Service) { s.ID = "../escape" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			service := base()
			mutate(&service)
			if err := service.validate(); !errors.Is(err, ErrInvalidConfig) && !errors.Is(err, ErrUnknownEngine) {
				t.Errorf("got %v, want a config error", err)
			}
		})
	}
}

// MongoDB is the one engine the panel cannot put a password on, so it must not
// be allowed onto an address anything else can reach.
func TestMongoServiceMustStayOnLoopback(t *testing.T) {
	service := Service{
		ID: "mongodb", Name: "mongo", Engine: EngineMongoDB,
		Database: "plan", Port: 27017, Bind: "0.0.0.0",
	}
	if err := service.validate(); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("got %v, want ErrInvalidConfig", err)
	}

	service.Bind = "127.0.0.1"
	service.User, service.Password = "someone", "ignored-password"
	if err := service.validate(); err != nil {
		t.Fatalf("loopback mongo should validate: %v", err)
	}
	// The credentials are cleared rather than kept: showing an account that was
	// never created would send an operator to a login that cannot work.
	if service.User != "" || service.Password != "" {
		t.Errorf("mongo kept credentials it cannot create: %q/%q", service.User, service.Password)
	}
}

func TestConnectionURI(t *testing.T) {
	mysql := Service{
		Engine: EngineMySQL, Bind: "127.0.0.1", Port: 3306,
		Database: "survival", User: "hypercraft", Password: "pw12345678",
	}
	if got, want := mysql.ConnectionURI(), "mysql://hypercraft:pw12345678@127.0.0.1:3306/survival"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if got, want := mysql.JDBCURL(), "jdbc:mysql://127.0.0.1:3306/survival"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}

	// A wildcard bind is not something a client dials.
	wildcard := mysql
	wildcard.Bind = "0.0.0.0"
	if !strings.Contains(wildcard.ConnectionURI(), "127.0.0.1") {
		t.Errorf("a wildcard bind should be reported as loopback: %q", wildcard.ConnectionURI())
	}

	mongo := Service{Engine: EngineMongoDB, Bind: "127.0.0.1", Port: 27017, Database: "plan"}
	if got, want := mongo.ConnectionURI(), "mongodb://127.0.0.1:27017/plan"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if mongo.JDBCURL() != "" {
		t.Errorf("mongo has no JDBC url, got %q", mongo.JDBCURL())
	}
}

func TestInstallIDRoundTrip(t *testing.T) {
	for _, tc := range []struct{ engine, version string }{
		{EngineMySQL, "8.0.45"},
		{EnginePostgreSQL, "17.6"},
		{EngineMongoDB, "8.0.28"},
	} {
		id := installID(tc.engine, tc.version)
		engine, version, ok := splitInstallID(id)
		if !ok || engine != tc.engine || version != tc.version {
			t.Errorf("%s round-tripped to %q/%q (ok=%v)", id, engine, version, ok)
		}
	}
	// A directory that is not one of ours is ignored rather than listed as an
	// engine with a made-up name.
	if _, _, ok := splitInstallID("mariadb-11.4"); ok {
		t.Errorf("an unknown engine directory should not parse")
	}
	if _, _, ok := splitInstallID("mysql-"); ok {
		t.Errorf("an install with no version should not parse")
	}
}

// The generated SQL is built by hand, so the password has to be free of
// anything that could end the string it lands in.
func TestMySQLBootstrapIsWellFormed(t *testing.T) {
	service := Service{
		Engine: EngineMySQL, Database: "survival",
		User: "hypercraft", Password: "aB3-xY7_qq99",
	}
	sql := mysqlBootstrap(service)
	for _, want := range []string{
		"ALTER USER 'root'@'localhost' IDENTIFIED BY 'aB3-xY7_qq99'",
		"CREATE DATABASE IF NOT EXISTS `survival`",
		"CREATE USER IF NOT EXISTS 'hypercraft'@'%'",
		"GRANT ALL PRIVILEGES ON `survival`.* TO 'hypercraft'@'%'",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("bootstrap SQL is missing %q:\n%s", want, sql)
		}
	}
	if err := validPassword(service.Password); err != nil {
		t.Errorf("the fixture password should be acceptable: %v", err)
	}
}

// Every password the panel generates itself has to survive its own validation,
// or creating a service without typing one would fail at random.
func TestGeneratedPasswordsValidate(t *testing.T) {
	for range 200 {
		password, err := generatePassword()
		if err != nil {
			t.Fatalf("generate: %v", err)
		}
		if err := validPassword(password); err != nil {
			t.Fatalf("generated %q, which does not validate: %v", password, err)
		}
	}
}

func TestMongoTargetsPreferTheActualDistro(t *testing.T) {
	targets := mongoTargets(Platform{OS: "linux", Arch: "amd64", Distro: "ubuntu", DistroVersion: "22.04"})
	if targets[0] != "ubuntu2204" {
		t.Errorf("the exact distro should come first, got %v", targets[:3])
	}
	// The fallback runs oldest-first: an older build loads on a newer system,
	// never the other way round.
	fallback := mongoTargets(Platform{OS: "linux", Arch: "amd64"})
	if fallback[0] != "ubuntu2004" {
		t.Errorf("the fallback should start with the oldest build, got %v", fallback[:3])
	}
	if got := mongoTargets(Platform{OS: "windows", Arch: "amd64"}); len(got) != 1 || got[0] != "windows" {
		t.Errorf("windows should have exactly one target, got %v", got)
	}
}

func TestPickMongoDownloadSkipsEnterprise(t *testing.T) {
	asset := func(edition, target, arch, url string) mongoAsset {
		entry := mongoAsset{Edition: edition, Target: target, Arch: arch}
		entry.Archive.URL = url
		entry.Archive.SHA256 = "abc"
		return entry
	}
	downloads := []mongoAsset{
		asset("enterprise", "ubuntu2204", "x86_64", "https://example.invalid/enterprise.tgz"),
		asset("targeted", "ubuntu2204", "x86_64", "https://example.invalid/community.tgz"),
	}

	found, err := pickMongoDownload(downloads, Platform{OS: "linux", Arch: "amd64", Distro: "ubuntu", DistroVersion: "22.04"})
	if err != nil {
		t.Fatalf("pick: %v", err)
	}
	if !strings.Contains(found.url, "community") {
		t.Errorf("the licensed build was chosen: %s", found.url)
	}

	// An architecture with no community build is an error, not a silent
	// fallback to something that will not run.
	if _, err := pickMongoDownload(downloads, Platform{OS: "linux", Arch: "arm64"}); !errors.Is(err, ErrUnsupported) {
		t.Errorf("got %v, want ErrUnsupported", err)
	}
}

func TestMySQLFileNames(t *testing.T) {
	cases := []struct {
		platform Platform
		want     string
	}{
		{Platform{OS: "linux", Arch: "amd64"}, "mysql-8.0.45-linux-glibc2.17-x86_64-minimal.tar.xz"},
		{Platform{OS: "linux", Arch: "arm64"}, "mysql-8.0.45-linux-glibc2.28-aarch64.tar.xz"},
		{Platform{OS: "windows", Arch: "amd64"}, "mysql-8.0.45-winx64.zip"},
	}
	for _, tc := range cases {
		got, err := mysqlFileName("8.0.45", tc.platform)
		if err != nil || got != tc.want {
			t.Errorf("%s/%s: got %q (%v), want %q", tc.platform.OS, tc.platform.Arch, got, err, tc.want)
		}
	}
	if _, err := mysqlFileName("8.0.45", Platform{OS: "darwin", Arch: "arm64"}); !errors.Is(err, ErrUnsupported) {
		t.Errorf("macOS should be reported as unsupported, got %v", err)
	}
}

func TestPostgresPlatformIDs(t *testing.T) {
	cases := map[string]Platform{
		"linux-amd64":        {OS: "linux", Arch: "amd64"},
		"linux-arm64v8":      {OS: "linux", Arch: "arm64"},
		"linux-amd64-alpine": {OS: "linux", Arch: "amd64", Musl: true},
		"darwin-arm64v8":     {OS: "darwin", Arch: "arm64"},
		"windows-amd64":      {OS: "windows", Arch: "amd64"},
	}
	for want, platform := range cases {
		if got := pgPlatformID(platform); got != want {
			t.Errorf("%+v: got %q, want %q", platform, got, want)
		}
	}
}

func TestPGVersionOf(t *testing.T) {
	if got := pgVersionOf("17.6.0"); got != "17.6" {
		t.Errorf("got %q, want 17.6", got)
	}
	for _, bad := range []string{"17.6", "17.6.0-beta", "1.2.3.4", "x.y.z", ""} {
		if got := pgVersionOf(bad); got != "" {
			t.Errorf("%q should be skipped, got %q", bad, got)
		}
	}
}

func TestCompareVersions(t *testing.T) {
	if compareVersions("8.0.45", "8.0.9") <= 0 {
		t.Error("8.0.45 should sort above 8.0.9")
	}
	if compareVersions("17", "17.6") >= 0 {
		t.Error("a missing component should count as zero")
	}
	if compareVersions("8.4.10", "8.4.10") != 0 {
		t.Error("equal versions should compare equal")
	}
}

// A dynamic-linker error is the failure mode of every one of these tarballs,
// and the hint is the difference between a fixable problem and a mystery.
func TestRunHint(t *testing.T) {
	hint := runHint("mysqld: error while loading shared libraries: libaio.so.1: cannot open shared object file")
	if !strings.Contains(hint, "libaio1") {
		t.Errorf("the libaio hint should name the package: %q", hint)
	}
	if runHint("mysqld  Ver 8.0.45") != "" {
		t.Error("ordinary output should produce no hint")
	}
}
