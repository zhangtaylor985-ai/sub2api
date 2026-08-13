package sessiondelivery

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/klauspost/compress/zstd"
)

const (
	projectionJSONKeyMaxBytes = 256
	projectionJSONMaxDepth    = 10000
)

func decodeStoredProjectionEnvelope(
	decoder *zstd.Decoder,
	compressed []byte,
	expectedSHA string,
	maxDecodedBytes int64,
) (*storedProjectionEnvelope, error) {
	if decoder == nil {
		return nil, errors.New("Session projection decoder is required")
	}
	if maxDecodedBytes <= 0 {
		return nil, errors.New("Session projection decoded byte limit must be positive")
	}
	if err := decoder.Reset(bytes.NewReader(compressed)); err != nil {
		return nil, fmt.Errorf("open Session projection payload: %w", err)
	}
	limited := &io.LimitedReader{R: decoder, N: maxDecodedBytes + 1}
	digest := sha256.New()
	reader := newProjectionJSONReader(io.TeeReader(limited, digest), maxDecodedBytes)
	record, err := reader.decodeEnvelope()
	if err != nil {
		if limited.N == 0 {
			return nil, fmt.Errorf("decoded Session projection envelope exceeds %d bytes", maxDecodedBytes)
		}
		return nil, fmt.Errorf("decode stored Session projection envelope: %w", err)
	}
	if limited.N == 0 {
		return nil, fmt.Errorf("decoded Session projection envelope exceeds %d bytes", maxDecodedBytes)
	}
	actualSHA := hex.EncodeToString(digest.Sum(nil))
	if actualSHA != strings.TrimSpace(expectedSHA) {
		return nil, fmt.Errorf("Session payload checksum mismatch: expected=%s actual=%s", expectedSHA, actualSHA)
	}
	if strings.TrimSpace(record.RecordID) == "" {
		return nil, errors.New("stored Session projection envelope is missing record_id")
	}
	return record, nil
}

// projectionJSONReader extracts three top-level fields without buffering the
// complete envelope. Unknown values (notably Original request/response audit
// payloads) are structurally skipped byte by byte. Target values are still
// decoded by encoding/json, so the delivery projection retains strict JSON
// validation while peak allocation follows delivery size rather than the much
// larger internal envelope size.
type projectionJSONReader struct {
	reader           *bufio.Reader
	maxCapturedBytes int64
}

func newProjectionJSONReader(reader io.Reader, maxCapturedBytes int64) *projectionJSONReader {
	return &projectionJSONReader{
		reader:           bufio.NewReaderSize(reader, 64<<10),
		maxCapturedBytes: maxCapturedBytes,
	}
}

func (r *projectionJSONReader) decodeEnvelope() (*storedProjectionEnvelope, error) {
	first, err := r.readNonSpaceByte()
	if err != nil {
		return nil, err
	}
	if first != '{' {
		return nil, errors.New("stored Session projection envelope must be a JSON object")
	}
	record := &storedProjectionEnvelope{}
	seen := make(map[string]struct{}, 3)
	for {
		next, err := r.peekNonSpaceByte()
		if err != nil {
			return nil, err
		}
		if next == '}' {
			_, _ = r.readNonSpaceByte()
			break
		}
		keyRaw, err := r.readJSONString(projectionJSONKeyMaxBytes)
		if err != nil {
			return nil, fmt.Errorf("read projection envelope key: %w", err)
		}
		var key string
		if err := json.Unmarshal(keyRaw, &key); err != nil {
			return nil, fmt.Errorf("decode projection envelope key: %w", err)
		}
		colon, err := r.readNonSpaceByte()
		if err != nil {
			return nil, err
		}
		if colon != ':' {
			return nil, errors.New("projection envelope key is missing a colon")
		}

		switch key {
		case "record_id", "delivery", "rejection":
			if _, duplicate := seen[key]; duplicate {
				return nil, fmt.Errorf("projection envelope contains duplicate field %q", key)
			}
			seen[key] = struct{}{}
			raw, err := r.readValue(true)
			if err != nil {
				return nil, fmt.Errorf("read projection field %s: %w", key, err)
			}
			switch key {
			case "record_id":
				if err := json.Unmarshal(raw, &record.RecordID); err != nil {
					return nil, fmt.Errorf("decode projection record_id: %w", err)
				}
			case "delivery":
				if err := json.Unmarshal(raw, &record.Delivery); err != nil {
					return nil, fmt.Errorf("decode projection delivery: %w", err)
				}
			case "rejection":
				if err := json.Unmarshal(raw, &record.Rejection); err != nil {
					return nil, fmt.Errorf("decode projection rejection: %w", err)
				}
			}
		default:
			if _, err := r.readValue(false); err != nil {
				return nil, fmt.Errorf("skip projection field %s: %w", key, err)
			}
		}

		delimiter, err := r.readNonSpaceByte()
		if err != nil {
			return nil, err
		}
		switch delimiter {
		case ',':
			next, err := r.peekNonSpaceByte()
			if err != nil {
				return nil, err
			}
			if next == '}' {
				return nil, errors.New("projection envelope contains a trailing comma")
			}
			continue
		case '}':
			if trailing, err := r.readNonSpaceByte(); !errors.Is(err, io.EOF) {
				if err != nil {
					return nil, err
				}
				return nil, fmt.Errorf("projection envelope has trailing byte %q", trailing)
			}
			return record, nil
		default:
			return nil, fmt.Errorf("projection envelope has invalid delimiter %q", delimiter)
		}
	}
	if trailing, err := r.readNonSpaceByte(); !errors.Is(err, io.EOF) {
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("projection envelope has trailing byte %q", trailing)
	}
	return record, nil
}

func (r *projectionJSONReader) readJSONString(maxBytes int64) ([]byte, error) {
	first, err := r.readNonSpaceByte()
	if err != nil {
		return nil, err
	}
	if first != '"' {
		return nil, errors.New("expected JSON string")
	}
	var captured bytes.Buffer
	captured.WriteByte(first)
	escaped := false
	for {
		value, err := r.reader.ReadByte()
		if err != nil {
			return nil, errors.New("unterminated JSON string")
		}
		if int64(captured.Len()) >= maxBytes {
			return nil, errors.New("JSON string exceeds limit")
		}
		captured.WriteByte(value)
		if escaped {
			escaped = false
			continue
		}
		if value == '\\' {
			escaped = true
			continue
		}
		if value == '"' {
			return captured.Bytes(), nil
		}
	}
}

func (r *projectionJSONReader) readValue(capture bool) ([]byte, error) {
	first, err := r.readNonSpaceByte()
	if err != nil {
		return nil, err
	}
	var captured bytes.Buffer
	write := func(value byte) error {
		if !capture {
			return nil
		}
		if int64(captured.Len()) >= r.maxCapturedBytes {
			return errors.New("captured projection value exceeds limit")
		}
		return captured.WriteByte(value)
	}
	if err := write(first); err != nil {
		return nil, err
	}

	switch first {
	case '{', '[':
		stack := []byte{first}
		inString := false
		escaped := false
		for len(stack) > 0 {
			value, err := r.reader.ReadByte()
			if err != nil {
				return nil, errors.New("unterminated composite JSON value")
			}
			if err := write(value); err != nil {
				return nil, err
			}
			if inString {
				if escaped {
					escaped = false
					continue
				}
				if value == '\\' {
					escaped = true
				} else if value == '"' {
					inString = false
				}
				continue
			}
			switch value {
			case '"':
				inString = true
			case '{', '[':
				if len(stack) >= projectionJSONMaxDepth {
					return nil, errors.New("projection JSON nesting exceeds limit")
				}
				stack = append(stack, value)
			case '}', ']':
				expected := byte('{')
				if value == ']' {
					expected = '['
				}
				if stack[len(stack)-1] != expected {
					return nil, errors.New("projection JSON has mismatched delimiters")
				}
				stack = stack[:len(stack)-1]
			}
		}
	case '"':
		escaped := false
		for {
			value, err := r.reader.ReadByte()
			if err != nil {
				return nil, errors.New("unterminated JSON string value")
			}
			if err := write(value); err != nil {
				return nil, err
			}
			if escaped {
				escaped = false
				continue
			}
			if value == '\\' {
				escaped = true
			} else if value == '"' {
				break
			}
		}
	default:
		if isProjectionJSONDelimiter(first) {
			return nil, fmt.Errorf("invalid JSON value prefix %q", first)
		}
		for {
			value, err := r.reader.ReadByte()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return nil, err
			}
			if isProjectionJSONDelimiter(value) {
				if err := r.reader.UnreadByte(); err != nil {
					return nil, err
				}
				break
			}
			if err := write(value); err != nil {
				return nil, err
			}
		}
	}
	if !capture {
		return nil, nil
	}
	return captured.Bytes(), nil
}

func (r *projectionJSONReader) readNonSpaceByte() (byte, error) {
	for {
		value, err := r.reader.ReadByte()
		if err != nil {
			return 0, err
		}
		if !isProjectionJSONWhitespace(value) {
			return value, nil
		}
	}
}

func (r *projectionJSONReader) peekNonSpaceByte() (byte, error) {
	value, err := r.readNonSpaceByte()
	if err != nil {
		return 0, err
	}
	if err := r.reader.UnreadByte(); err != nil {
		return 0, err
	}
	return value, nil
}

func isProjectionJSONWhitespace(value byte) bool {
	return value == ' ' || value == '\t' || value == '\r' || value == '\n'
}

func isProjectionJSONDelimiter(value byte) bool {
	return isProjectionJSONWhitespace(value) || value == ',' || value == '}' || value == ']'
}
