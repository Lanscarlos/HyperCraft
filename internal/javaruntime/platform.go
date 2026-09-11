// Package javaruntime manages the Java runtimes the panel launches servers
// with: what is installed, what the system provides, and fetching new ones
// from Eclipse Adoptium.
//
// It exists because Minecraft's Java requirement moves with the version —
// 1.16 wants Java 8, 1.17 wants 16, 1.20.5 wants 21, Paper 26 wants 25 — and
// "which java is on this box" is not something an operator should have to
// solve with a package manager before they can start a server.
package javaruntime

import (
	"fmt"
	"path/filepath"
	"runtime"
)

// Platform is the OS/architecture pair to ask a distribution for.
type Platform struct {
	OS   string `json:"os"`
	Arch string `json:"arch"`
	// LibC is "glibc" or "musl" on Linux, empty elsewhere. Zulu builds for
	// both; Temurin only for glibc.
	LibC string `json:"libc,omitempty"`
	// Warning is a non-fatal note about installing on this platform. Whether
	// there is one depends on which distribution is selected, so it is filled
	// in by PlatformWarning rather than by CurrentPlatform.
	Warning string `json:"warning,omitempty"`
}

// osNames maps Go's GOOS onto the os parameter both metadata APIs take. Azul
// accepts Adoptium's spellings (it takes "mac" as well as its own "macos"), so
// one table covers both.
var osNames = map[string]string{
	"linux":   "linux",
	"darwin":  "mac",
	"windows": "windows",
	"aix":     "aix",
}

// archNames maps Go's GOARCH onto the architecture parameter. As with osNames,
// Azul accepts these spellings too.
var archNames = map[string]string{
	"amd64": "x64",
	"arm64": "aarch64",
	"386":   "x86",
	"arm":   "arm",
	"ppc64": "ppc64",
	"s390x": "s390x",
}

// CurrentPlatform describes the machine the panel is running on.
func CurrentPlatform() (Platform, error) {
	osName, ok := osNames[runtime.GOOS]
	if !ok {
		return Platform{}, fmt.Errorf("%w: 没有 %s 的 Java 构建", ErrUnsupported, runtime.GOOS)
	}
	arch, ok := archNames[runtime.GOARCH]
	if !ok {
		return Platform{}, fmt.Errorf("%w: 没有 %s/%s 的 Java 构建", ErrUnsupported, runtime.GOOS, runtime.GOARCH)
	}
	return Platform{OS: osName, Arch: arch, LibC: detectLibC()}, nil
}

// detectLibC says which C library this Linux runs. Alpine and friends ship
// musl, where a glibc-linked JDK unpacks fine and then fails to start with a
// dynamic-linker error.
func detectLibC() string {
	if runtime.GOOS != "linux" {
		return ""
	}
	matches, err := filepath.Glob("/lib/ld-musl-*.so.1")
	if err != nil || len(matches) == 0 {
		return "glibc"
	}
	return "musl"
}

// PlatformWarning flags a combination where the download would install fine
// and then fail to run — better to say so before a 50 MB download than after.
//
// It takes the distribution because the only such combination left is
// Temurin's: it links against glibc, so on musl every binary in the tarball is
// a dynamic-linker error waiting to happen. Zulu ships a musl build, which is
// why the way out is now a switch inside the panel rather than a package
// manager and a hand-typed path.
func PlatformWarning(dist string, platform Platform) string {
	if dist != DistTemurin || platform.LibC != "musl" {
		return ""
	}
	return "这台机器用的是 musl（Alpine 之类），Temurin 是 glibc 构建，装上也跑不起来。" +
		"在「下载设置」里把发行版换成 Azul Zulu 就能装 —— 它有 musl 构建。"
}

// javaBinary is the executable name inside a runtime's bin directory.
func javaBinary() string {
	if runtime.GOOS == "windows" {
		return "java.exe"
	}
	return "java"
}

// javacBinary is the compiler's name inside a runtime's bin directory. Whether
// it is there is what actually separates a JDK from a JRE.
func javacBinary() string {
	if runtime.GOOS == "windows" {
		return "javac.exe"
	}
	return "javac"
}
