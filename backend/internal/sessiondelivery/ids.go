package sessiondelivery

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

const minimumHMACSecretBytes = 32

type IDGenerator struct {
	secret  []byte
	aliases *FileAliasStore
}

func NewIDGenerator(secret string, aliases *FileAliasStore) (*IDGenerator, error) {
	if len(secret) < minimumHMACSecretBytes {
		return nil, fmt.Errorf("session delivery HMAC secret must be at least %d bytes", minimumHMACSecretBytes)
	}
	return &IDGenerator{secret: []byte(secret), aliases: aliases}, nil
}

func (g *IDGenerator) RecordID(scope Scope, gatewayRequestID string) string {
	return g.derive("rec_", "record", scopeKey(scope), gatewayRequestID)
}

func (g *IDGenerator) RequestID(scope Scope, gatewayRequestID string) string {
	return g.derive("req_", "request", scopeKey(scope), gatewayRequestID)
}

func (g *IDGenerator) ResponseID(scope Scope, sourceResponseID, publicRequestID string) string {
	seed := strings.TrimSpace(sourceResponseID)
	if seed == "" {
		seed = publicRequestID
	}
	return g.derive("msg_", "response", scopeKey(scope), seed)
}

func (g *IDGenerator) ResolveSession(
	protocol Protocol,
	scope Scope,
	sessionHeader string,
	request json.RawMessage,
	response json.RawMessage,
	publicRequestID string,
) (string, error) {
	signalKind, signal, previousResponseID := extractSessionSignals(protocol, sessionHeader, request)
	scopeValue := scopeKey(scope)

	var sessionID string
	if signal != "" {
		sessionID = g.derive("session_", "session", scopeValue, string(protocol), signalKind, signal)
	} else if previousResponseID != "" && g.aliases != nil {
		resolved, err := g.aliases.Lookup(scopeValue, previousResponseID)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("lookup session alias: %w", err)
		}
		sessionID = resolved
	}
	if sessionID == "" {
		sessionID = g.derive("session_", "session-fallback", scopeValue, string(protocol), publicRequestID)
	}

	if responseID := extractJSONText(response, "id"); responseID != "" && g.aliases != nil {
		if err := g.aliases.Bind(scopeValue, responseID, sessionID); err != nil {
			return "", fmt.Errorf("bind session alias: %w", err)
		}
	}
	return sessionID, nil
}

func (g *IDGenerator) derive(prefix string, parts ...string) string {
	mac := hmac.New(sha256.New, g.secret)
	for _, part := range parts {
		_, _ = mac.Write([]byte(strconv.Itoa(len(part))))
		_, _ = mac.Write([]byte{':'})
		_, _ = mac.Write([]byte(part))
		_, _ = mac.Write([]byte{'|'})
	}
	return prefix + hex.EncodeToString(mac.Sum(nil)[:16])
}

func scopeKey(scope Scope) string {
	return fmt.Sprintf("user=%d;key=%d;group=%d", scope.UserID, scope.APIKeyID, scope.GroupID)
}

func extractSessionSignals(protocol Protocol, sessionHeader string, request json.RawMessage) (kind, signal, previous string) {
	if value := boundedSignal(sessionHeader); value != "" {
		return "header", value, ""
	}

	var body map[string]json.RawMessage
	if json.Unmarshal(request, &body) != nil {
		return "", "", ""
	}

	if protocol == ProtocolOpenAIResponses {
		if value := boundedSignal(rawString(body["prompt_cache_key"])); value != "" {
			return "prompt_cache_key", value, rawString(body["previous_response_id"])
		}
		if value := boundedSignal(extractConversationID(body["conversation"])); value != "" {
			return "conversation", value, rawString(body["previous_response_id"])
		}
		return "", "", boundedSignal(rawString(body["previous_response_id"]))
	}

	if metadata := body["metadata"]; len(metadata) > 0 {
		var values map[string]json.RawMessage
		if json.Unmarshal(metadata, &values) == nil {
			for _, key := range []string{"session_id", "user_id"} {
				if value := boundedSignal(rawString(values[key])); value != "" {
					return "metadata." + key, value, ""
				}
			}
		}
	}
	return "", "", ""
}

func extractConversationID(raw json.RawMessage) string {
	if value := rawString(raw); value != "" {
		return value
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) == nil {
		return rawString(object["id"])
	}
	return ""
}

func boundedSignal(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 4096 {
		return value[:4096]
	}
	return value
}

func rawString(raw json.RawMessage) string {
	var value string
	if len(raw) > 0 && json.Unmarshal(raw, &value) == nil {
		return value
	}
	return ""
}

func extractJSONText(raw json.RawMessage, key string) string {
	var body map[string]json.RawMessage
	if json.Unmarshal(raw, &body) != nil {
		return ""
	}
	return rawString(body[key])
}

// FileAliasStore persists response_id -> session_id links across gateway
// restarts without introducing another online dependency in the request path.
type FileAliasStore struct {
	dir    string
	secret []byte
	mu     sync.Mutex
}

func NewFileAliasStore(dir, secret string) (*FileAliasStore, error) {
	if len(secret) < minimumHMACSecretBytes {
		return nil, fmt.Errorf("alias HMAC secret must be at least %d bytes", minimumHMACSecretBytes)
	}
	if strings.TrimSpace(dir) == "" {
		return nil, errors.New("alias directory is required")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create alias directory: %w", err)
	}
	return &FileAliasStore{dir: dir, secret: []byte(secret)}, nil
}

func (s *FileAliasStore) Lookup(scope, responseID string) (string, error) {
	path := s.path(scope, responseID)
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	value := strings.TrimSpace(string(data))
	if !strings.HasPrefix(value, "session_") || len(value) > 128 {
		return "", errors.New("invalid session alias value")
	}
	return value, nil
}

func (s *FileAliasStore) Bind(scope, responseID, sessionID string) error {
	if strings.TrimSpace(responseID) == "" || !strings.HasPrefix(sessionID, "session_") {
		return errors.New("invalid session alias binding")
	}
	path := s.path(scope, responseID)
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, err := os.ReadFile(path); err == nil {
		if strings.TrimSpace(string(existing)) == sessionID {
			return nil
		}
		return errors.New("session alias is already bound to a different session")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".alias-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.WriteString(sessionID + "\n"); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	// Link commits without replacing an alias another gateway process may
	// have created between the existence check and this point. The temp file
	// is in the same directory, so the operation is atomic on the production
	// filesystem.
	if err := os.Link(tmpName, path); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return err
		}
		existing, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if strings.TrimSpace(string(existing)) != sessionID {
			return errors.New("session alias is already bound to a different session")
		}
	}
	return syncDirectory(filepath.Dir(path))
}

func (s *FileAliasStore) path(scope, responseID string) string {
	mac := hmac.New(sha256.New, s.secret)
	_, _ = mac.Write([]byte(scope))
	_, _ = mac.Write([]byte{'|'})
	_, _ = mac.Write([]byte(responseID))
	digest := hex.EncodeToString(mac.Sum(nil))
	return filepath.Join(s.dir, digest[:2], digest+".alias")
}
