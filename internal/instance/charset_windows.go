//go:build windows

package instance

import (
	"sync"
	"syscall"

	"golang.org/x/text/encoding"
)

// codePageCharsets maps the Windows ANSI code pages people actually run
// Minecraft on to the charset a JVM without UTF-8 flags will print in.
var codePageCharsets = map[int]string{
	936:   "gbk",          // 简体中文
	950:   "big5",         // 繁體中文
	932:   "shift_jis",    // 日本語
	949:   "euc-kr",       // 한국어
	54936: "gb18030",      //
	1252:  "windows-1252", // Western European
	65001: EncodingUTF8,
}

var (
	kernel32   = syscall.NewLazyDLL("kernel32.dll")
	procGetACP = kernel32.NewProc("GetACP")

	systemCharsetOnce sync.Once
	systemCharsetEnc  encoding.Encoding
)

// systemCharset returns the encoding a process that ignores UTF-8 writes its
// console output in, or nil when that is UTF-8 already. Only auto mode uses
// it, and only for lines that are not valid UTF-8.
func systemCharset() encoding.Encoding {
	systemCharsetOnce.Do(func() {
		acp, _, _ := procGetACP.Call()
		name, ok := codePageCharsets[int(acp)]
		if !ok {
			return
		}
		systemCharsetEnc = charsets[name]
	})
	return systemCharsetEnc
}
