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

// Platform is the OS/architecture pair to ask Adoptium for.
type Platform struct {
	OS   string `json:"os"`
	Arch string `json:"arch"`
	// Warning is a non-fatal note about this platform, e.g. musl.
	Warning string `json:"warning,omitempty"`
}

// osNames maps Go's GOOS onto Adoptium's os parameter.
var osNames = map[string]string{
	"linux":   "linux",
	"darwin":  "mac",
	"windows": "windows",
	"aix":     "aix",
}

// archNames maps Go's GOARCH onto Adoptium's architecture parameter.
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
		return Platform{}, fmt.Errorf("%w: Adoptium 没有 %s 的构建", ErrUnsupported, runtime.GOOS)
	}
	arch, ok := archNames[runtime.GOARCH]
	if !ok {
		return Platform{}, fmt.Errorf("%w: Adoptium 没有 %s/%s 的构建", ErrUnsupported, runtime.GOOS, runtime.GOARCH)
	}
	return Platform{OS: osName, Arch: arch, Warning: platformWarning()}, nil
}

// platformWarning flags a system where the download would install fine and
// then fail to run. Temurin links against glibc, so on a musl distro (Alpine
// and friends) every binary in the tarball is a dynamic-linker error waiting
// to happen — better to say so before a 50 MB download than after.
func platformWarning() string {
	if runtime.GOOS != "linux" {
		return ""
	}
	matches, err := filepath.Glob("/lib/ld-musl-*.so.1")
	if err != nil || len(matches) == 0 {
		return ""
	}
	return "这台机器用的是 musl（Alpine 之类），Temurin 是 glibc 构建，装上也跑不起来。" +
		"请用系统包管理器装 OpenJDK，或换 Liberica 的 musl 版本，再在启动设置里手填路径。"
}

// javaBinary is the executable name inside a runtime's bin directory.
func javaBinary() string {
	if runtime.GOOS == "windows" {
		return "java.exe"
	}
	return "java"
}
