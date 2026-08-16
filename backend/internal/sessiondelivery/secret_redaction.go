package sessiondelivery

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
)

// A real troubleshooting session reads the user's own configuration, so the
// tool output carrying that file — and the assistant's discussion of it — can
// contain live credentials. The vendor specification names redaction as the one
// sanctioned exception to leaving the request alone, so credentials are masked
// rather than dropping the records that mention them.
//
// Redaction differs from every other rewrite in this package in two ways. It
// covers the response as well as the request, because a secret masked in one
// place and left in another is not masked at all. And it is applied to the
// encoded bytes: the shapes below contain no character JSON escapes, so a byte
// replacement reaches every occurrence — tool results, tool inputs, assistant
// prose — while leaving the surrounding document untouched.

const secretRedactionDomain = "sub2api-session-delivery-secret-redaction-v1"

// secretShapes are measured. Each requires an unbroken run of secret-looking
// characters, which is what separates a generated credential from an
// identifier that merely contains the word "token": the corpus carries 660
// matches for a loose key/token/secret assignment pattern, and every one of
// them is a Python variable such as state_tokens_before_call.
var secretShapes = []struct {
	name    string
	pattern *regexp.Regexp
	prefix  string
}{
	{
		// MEASURED: api_key assignments in the user's config files. Requiring
		// an unbroken alphanumeric run excludes hyphenated slugs such as
		// sk-chat-v2-frontend-api.
		name:    "sk-key",
		pattern: regexp.MustCompile(`\bsk-[A-Za-z0-9]{24,}\b`),
		prefix:  "sk-",
	},
	{
		// MEASURED: the gateway token discussed in the session, written both as
		// "Bearer <token>" and as a bare authorization header value. The dotted
		// suffix is what distinguishes it from an ordinary UUID.
		name:    "uuid-token",
		pattern: regexp.MustCompile(`\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\.[0-9a-f]{6,}\b`),
		prefix:  "",
	},
}

// redactionLabel maps a secret to a stable, obviously-masked stand-in. The
// label is derived so the same credential reads the same everywhere in the
// corpus and two different credentials stay distinguishable, which keeps a
// conversation about one of them coherent.
func redactionLabel(prefix string, secret []byte) []byte {
	mac := hmac.New(sha256.New, []byte(secretRedactionDomain))
	_, _ = mac.Write(secret)
	digest := hex.EncodeToString(mac.Sum(nil))[:8]
	return []byte(prefix + "REDACTED-" + digest)
}

// redactSecrets masks every measured credential shape in an encoded document.
func redactSecrets(encoded []byte) ([]byte, int64) {
	var redacted int64
	for _, shape := range secretShapes {
		for _, secret := range shape.pattern.FindAll(encoded, -1) {
			if bytes.Contains(secret, []byte("REDACTED-")) {
				continue
			}
			count := bytes.Count(encoded, secret)
			encoded = bytes.ReplaceAll(encoded, secret, redactionLabel(shape.prefix, secret))
			redacted += int64(count)
		}
	}
	return encoded, redacted
}

// validateNoSecrets fails closed when a credential shape survives redaction.
func validateNoSecrets(documents ...[]byte) error {
	for _, document := range documents {
		for _, shape := range secretShapes {
			if match := shape.pattern.Find(document); match != nil {
				if bytes.Contains(match, []byte("REDACTED-")) {
					continue
				}
				return fmt.Errorf("delivery record still carries a %s credential", shape.name)
			}
		}
	}
	return nil
}
