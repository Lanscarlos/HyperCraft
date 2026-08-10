package plugin

import (
	"fmt"
	"strings"
	"sync"
)

// Token is one GitHub credential the panel holds.
//
// A list rather than a single string, because "the operator's GitHub account"
// is not one thing. A fine-grained personal access token is issued by one
// account and scoped to a set of that account's repositories: the token that
// reads the plugin in someone's own namespace cannot be granted access to the
// organisation's private fork, and a token an organisation issued should not be
// what the panel sends when it asks about a personal repository. With one
// credential the only way to cover both is a classic token with blanket repo
// scope — which is to say, the broadest credential GitHub can mint, held by a
// panel that only needs to read two repositories.
//
// So a source names the token it is read with, and the panel sends that one and
// no other.
type Token struct {
	// ID is what a Source names to pick this token. It is stable across renames
	// and across the secret being replaced — an id that changed when a token was
	// rotated would silently detach every plugin pointing at it.
	ID string
	// Name is the operator's label for it, used in errors so "the token cannot
	// read this repository" says which token.
	Name string
	// Secret is the credential itself. It goes to the API host this client was
	// built with and nowhere else — never to a download mirror, and never into
	// a log line. See Client.downloadOrder.
	Secret string
}

// tokenSet is the credential list a Client reads sources with.
//
// Ordered, and the first entry is the default: a source that names no token —
// which is every source added before tokens could be told apart, and every
// public repository, where a token buys rate limit rather than access — is read
// with it. Locked because the settings page can replace the whole list while a
// check is in flight.
type tokenSet struct {
	mu     sync.RWMutex
	tokens []Token
}

// set replaces the list. Entries with no id or no secret are dropped rather
// than stored: an id is how a source points at one of these, and a token with
// no secret would authenticate nothing while still looking configured.
func (t *tokenSet) set(tokens []Token) {
	cleaned := make([]Token, 0, len(tokens))
	seen := make(map[string]bool, len(tokens))
	for _, tok := range tokens {
		tok.ID = strings.TrimSpace(tok.ID)
		tok.Name = strings.TrimSpace(tok.Name)
		tok.Secret = strings.TrimSpace(tok.Secret)
		if tok.ID == "" || tok.Secret == "" || seen[tok.ID] {
			continue
		}
		seen[tok.ID] = true
		cleaned = append(cleaned, tok)
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	t.tokens = cleaned
}

// fallback is the default token, and whether there is one at all.
func (t *tokenSet) fallback() (Token, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if len(t.tokens) == 0 {
		return Token{}, false
	}
	return t.tokens[0], true
}

// resolve picks the token a source is read with.
//
// Naming no token is not an error: it means the default, and with no tokens
// configured at all it means anonymous, which is all a public repository needs.
// Naming one that is gone *is* an error, and deliberately not a silent fall
// back to the default — a source pinned to the organisation's token would
// otherwise start sending the operator's personal one the moment the first was
// deleted, and answer with a 404 that reads like the repository vanished.
func (t *tokenSet) resolve(id string) (Token, error) {
	if id = strings.TrimSpace(id); id == "" {
		token, _ := t.fallback()
		return token, nil
	}

	t.mu.RLock()
	defer t.mu.RUnlock()
	for _, token := range t.tokens {
		if token.ID == id {
			return token, nil
		}
	}
	return Token{}, fmt.Errorf("%w: 这个插件指定的访问令牌已经不在了 —— 去「面板设置 → GitHub 集成」挑一个现有的",
		ErrNeedsToken)
}
