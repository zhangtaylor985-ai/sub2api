package sessiondelivery

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"
)

const (
	projectionCheckpointVersion      = 1
	projectionCheckpointMaxJSONBytes = 256 << 20
)

// projectionCheckpoint is an internal continuation cursor for hourly exports.
// It contains no raw request text, credentials, routing data, or account data.
// Keeping it in the isolated database lets the next hour reproduce byte-exact
// thinking echoes and the prompt-cache chain after the prior record partition
// has been safely archived and purged.
type projectionCheckpoint struct {
	Version int                       `json:"version"`
	Echo    []projectionEchoTurn      `json:"echo,omitempty"`
	Usage   projectionUsageCheckpoint `json:"usage"`
}

type projectionEchoTurn struct {
	Key      string            `json:"key"`
	Thinking []json.RawMessage `json:"thinking"`
}

type projectionUsageCheckpoint struct {
	PreviousPrefix    int       `json:"previous_prefix,omitempty"`
	FirstMessageSHA   string    `json:"first_message_sha256,omitempty"`
	PreviousOccurred  time.Time `json:"previous_occurred_at,omitempty"`
	HasPreviousRecord bool      `json:"has_previous_record"`
}

type encodedProjectionCheckpoint struct {
	SessionID      string
	Version        int
	Compressed     []byte
	SHA256         string
	LastExportHour time.Time
}

func checkpointFromProjectors(echo *echoRepair, usage *usageProjector) projectionCheckpoint {
	checkpoint := projectionCheckpoint{Version: projectionCheckpointVersion}
	if echo != nil {
		checkpoint.Echo = echo.checkpoint()
	}
	if usage != nil {
		checkpoint.Usage = usage.checkpoint()
	}
	return checkpoint
}

func encodeProjectionCheckpoint(sessionID string, checkpoint projectionCheckpoint, hour time.Time) (encodedProjectionCheckpoint, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return encodedProjectionCheckpoint{}, errors.New("projection checkpoint session ID is required")
	}
	if err := validateProjectionCheckpoint(checkpoint); err != nil {
		return encodedProjectionCheckpoint{}, err
	}
	payload, err := json.Marshal(checkpoint)
	if err != nil {
		return encodedProjectionCheckpoint{}, fmt.Errorf("encode projection checkpoint: %w", err)
	}
	if len(payload) > projectionCheckpointMaxJSONBytes {
		return encodedProjectionCheckpoint{}, errors.New("projection checkpoint exceeds the size limit")
	}
	digest := sha256.Sum256(payload)
	encoder, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedDefault))
	if err != nil {
		return encodedProjectionCheckpoint{}, fmt.Errorf("create projection checkpoint encoder: %w", err)
	}
	compressed := encoder.EncodeAll(payload, nil)
	encoder.Close()
	return encodedProjectionCheckpoint{
		SessionID:      sessionID,
		Version:        checkpoint.Version,
		Compressed:     compressed,
		SHA256:         hex.EncodeToString(digest[:]),
		LastExportHour: hourUTC(hour),
	}, nil
}

func decodeProjectionCheckpoint(compressed []byte, expectedSHA string, version int) (projectionCheckpoint, error) {
	if len(compressed) == 0 {
		return projectionCheckpoint{}, errors.New("projection checkpoint payload is empty")
	}
	decoder, err := zstd.NewReader(nil, zstd.WithDecoderMaxMemory(projectionCheckpointMaxJSONBytes))
	if err != nil {
		return projectionCheckpoint{}, fmt.Errorf("create projection checkpoint decoder: %w", err)
	}
	payload, err := decoder.DecodeAll(compressed, nil)
	decoder.Close()
	if err != nil {
		return projectionCheckpoint{}, fmt.Errorf("decode projection checkpoint: %w", err)
	}
	if len(payload) > projectionCheckpointMaxJSONBytes {
		return projectionCheckpoint{}, errors.New("decoded projection checkpoint exceeds the size limit")
	}
	digest := sha256.Sum256(payload)
	if actual := hex.EncodeToString(digest[:]); actual != strings.TrimSpace(expectedSHA) {
		return projectionCheckpoint{}, errors.New("projection checkpoint checksum mismatch")
	}
	var checkpoint projectionCheckpoint
	if err := json.Unmarshal(payload, &checkpoint); err != nil {
		return projectionCheckpoint{}, fmt.Errorf("parse projection checkpoint: %w", err)
	}
	if checkpoint.Version != version {
		return projectionCheckpoint{}, errors.New("projection checkpoint version metadata mismatch")
	}
	if err := validateProjectionCheckpoint(checkpoint); err != nil {
		return projectionCheckpoint{}, err
	}
	return checkpoint, nil
}

func validateProjectionCheckpoint(checkpoint projectionCheckpoint) error {
	if checkpoint.Version != projectionCheckpointVersion {
		return fmt.Errorf("unsupported projection checkpoint version %d", checkpoint.Version)
	}
	for index, turn := range checkpoint.Echo {
		if !validProjectionEchoKey(turn.Key) || len(turn.Thinking) == 0 {
			return fmt.Errorf("invalid projection echo turn %d", index)
		}
		for _, raw := range turn.Thinking {
			if !json.Valid(raw) {
				return fmt.Errorf("projection echo turn %d contains invalid JSON", index)
			}
			var block map[string]json.RawMessage
			if json.Unmarshal(raw, &block) != nil || rawString(block["type"]) != "thinking" || rawString(block["signature"]) == "" {
				return fmt.Errorf("projection echo turn %d contains an invalid thinking block", index)
			}
		}
	}
	usage := checkpoint.Usage
	if usage.PreviousPrefix < 0 {
		return errors.New("projection usage prefix cannot be negative")
	}
	if !usage.HasPreviousRecord {
		if usage.PreviousPrefix != 0 || usage.FirstMessageSHA != "" || !usage.PreviousOccurred.IsZero() {
			return errors.New("empty projection usage checkpoint carries state")
		}
		return nil
	}
	if len(usage.FirstMessageSHA) != sha256.Size*2 {
		return errors.New("projection usage message hash is invalid")
	}
	if _, err := hex.DecodeString(usage.FirstMessageSHA); err != nil {
		return errors.New("projection usage message hash is invalid")
	}
	if usage.PreviousOccurred.IsZero() {
		return errors.New("projection usage timestamp is required")
	}
	return nil
}

func validProjectionEchoKey(key string) bool {
	for _, prefix := range []string{"text:", "content:"} {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		digest := strings.TrimPrefix(key, prefix)
		if len(digest) != sha256.Size*2 {
			return false
		}
		_, err := hex.DecodeString(digest)
		return err == nil
	}
	return false
}
