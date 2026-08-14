package sessiondelivery

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/thinkingsig"
)

const requestHistorySignatureDomain = "sub2api-session-request-history-opus5-v1"

// deterministicRequestHistorySignature upgrades an opaque client-history
// signature that cannot be echoed from an earlier captured response. The
// replacement is scoped to the stable delivery Session and source signature,
// so the same assistant turn remains byte-identical across later requests and
// hourly exports. This is delivery-only and never reaches the live client.
func deterministicRequestHistorySignature(sessionID, sourceSignature, model string, thinkingBytes int) string {
	model = strings.TrimSpace(model)
	if model == "" {
		model = DefaultPublicModel
	}
	seed := sha256.Sum256([]byte(requestHistorySignatureDomain + "\x00" + sessionID + "\x00" + sourceSignature))
	payloadLen := deterministicRequestHistoryPayloadLen(thinkingBytes, seed)

	var meta requestHistoryPBWriter
	meta.tagVarint(1, 16)
	meta.tagVarint(3, 2)
	meta.tagBytes(5, requestHistoryDerivedBytes(seed, "key-hash", 64))
	meta.tagBytes(6, []byte(model))
	meta.tagVarint(7, 1)
	meta.tagBytes(8, []byte("thinking"))
	meta.tagBytes(11, []byte(thinkingsig.DefaultReasoningUUID))

	var inner requestHistoryPBWriter
	inner.tagBytes(1, meta.buf)
	inner.tagBytes(2, requestHistoryDerivedBytes(seed, "nonce-a", 12))
	inner.tagBytes(3, requestHistoryDerivedBytes(seed, "nonce-b", 12))
	inner.tagBytes(4, requestHistoryDerivedBytes(seed, "wrapped-key", 48))
	inner.tagBytes(5, requestHistoryDerivedBytes(seed, "ciphertext", payloadLen))

	var outer requestHistoryPBWriter
	outer.tagVarint(1, 2)
	outer.tagBytes(2, inner.buf)
	outer.tagVarint(3, 1)
	return base64.StdEncoding.EncodeToString(outer.buf)
}

func deterministicRequestHistoryPayloadLen(thinkingBytes int, seed [sha256.Size]byte) int {
	if thinkingBytes < 0 {
		thinkingBytes = 0
	}
	// Approximately 1.75 ciphertext bytes per estimated four-byte token.
	payloadLen := thinkingBytes * 7 / 16
	if payloadLen < 800 {
		payloadLen = 800 + int(seed[0])%120
	}
	// Keep malformed client history from forcing an unbounded export-time
	// allocation while remaining well above observed real signature sizes.
	if payloadLen > 1<<20 {
		payloadLen = 1 << 20
	}
	return payloadLen
}

func requestHistoryDerivedBytes(seed [sha256.Size]byte, label string, size int) []byte {
	result := make([]byte, 0, size)
	for counter := uint32(0); len(result) < size; counter++ {
		mac := hmac.New(sha256.New, seed[:])
		_, _ = mac.Write([]byte(requestHistorySignatureDomain))
		_, _ = mac.Write([]byte{0})
		_, _ = mac.Write([]byte(label))
		var encodedCounter [4]byte
		binary.BigEndian.PutUint32(encodedCounter[:], counter)
		_, _ = mac.Write(encodedCounter[:])
		result = append(result, mac.Sum(nil)...)
	}
	return result[:size]
}

type requestHistoryPBWriter struct {
	buf []byte
}

func (writer *requestHistoryPBWriter) varint(value uint64) {
	for value >= 0x80 {
		writer.buf = append(writer.buf, byte(value)|0x80)
		value >>= 7
	}
	writer.buf = append(writer.buf, byte(value))
}

func (writer *requestHistoryPBWriter) tagVarint(field int, value uint64) {
	writer.varint(uint64(field) << 3)
	writer.varint(value)
}

func (writer *requestHistoryPBWriter) tagBytes(field int, value []byte) {
	writer.varint(uint64(field)<<3 | 2)
	writer.varint(uint64(len(value)))
	writer.buf = append(writer.buf, value...)
}
