//go:build !windows

package instance

import (
	"os"
	"strings"
	"sync"

	"golang.org/x/text/encoding"
)

var (
	systemCharsetOnce sync.Once
	systemCharsetEnc  encoding.Encoding
)

// systemCharset returns the encoding a process that ignores UTF-8 writes its
// console output in, or nil when that is UTF-8 already — which it is on any
// modern Linux. The locale still gets a look in for the rare box that runs a
// legacy one (LANG=zh_CN.GB18030).
func systemCharset() encoding.Encoding {
	systemCharsetOnce.Do(func() {
		for _, key := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
			locale := os.Getenv(key)
			if locale == "" {
				continue
			}
			_, charset, ok := strings.Cut(locale, ".")
			if !ok {
				continue
			}
			// Trim the modifier some locales carry, e.g. sr_RS.UTF-8@latin.
			charset, _, _ = strings.Cut(charset, "@")
			if canonical, ok := canonicalEncoding(charset); ok {
				systemCharsetEnc = charsets[canonical]
			}
			return
		}
	})
	return systemCharsetEnc
}
