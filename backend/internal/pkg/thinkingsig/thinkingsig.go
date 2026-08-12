// Package thinkingsig synthesizes Anthropic thinking.signature blobs in the
// exact wire shape produced by real Claude models, for the Claude -> OpenAI
// dispatch path where upstream Responses output carries no Anthropic signature.
//
// A real signature (base64-decoded) is a protobuf-framed AEAD envelope:
//
//	outer = { 1: scheme(=2, opus-5 only), 2: <inner>, 3: keyId(=1) }
//	inner = { 1: meta, 2: nonce(12B), 3: nonce(12B),
//	          4: wrappedKey(48B), 5: ciphertext+tag(variable) }
//	meta  = { 1: version(15=opus-4.x, 16=opus-5), 3: 2, 5: keyHash(64B),
//	          6: model, 7: envelopeFlag(0=opus-4.x, 1=opus-5),
//	          8: "thinking", 11: reasoning UUID }
//
// The encrypted regions (nonces / wrapped key / ciphertext / keyHash) are
// computationally indistinguishable from random bytes (measured entropy
// ~7.99 bits/byte, printable-ASCII ratio ~0.39), so they are filled with
// crypto/rand. The result is byte-shape identical to real output and cannot
// be distinguished by offline inspection; only Anthropic's server-side
// decryption can tell (we intentionally cannot pass that, and clients on
// this dispatch path never round-trip the signature back to Anthropic).
//
// Fidelity rules measured from real local Claude Code sessions:
//   - meta field 11 is a global Anthropic constant (DefaultReasoningUUID),
//     identical across sessions and across opus-4-8 / opus-5 models.
//   - meta field 5 (keyHash, 64B) is fresh per block.
//   - ciphertext length tracks reasoning volume: bytes/token in U(1.15, 2.40).
//   - base64 uses StdEncoding (padding appears naturally when length % 3 != 0).
package thinkingsig

import (
	"crypto/rand"
	"encoding/base64"
	mrand "math/rand/v2"
	"strings"
)

// DefaultReasoningUUID is the meta field-11 value observed in every real
// local signature (opus-4-8 and opus-5 alike). It is an Anthropic-side
// constant, not per-session, so generators should use it verbatim.
const DefaultReasoningUUID = "c24fa12f-1b38-4240-a074-bedadee4da32"

// Envelope layout versions, keyed off header field 1.
const (
	layoutOpus4 = 15 // claude-opus-4-*: no outer scheme field, meta f7=0
	layoutOpus5 = 16 // claude-opus-5:   outer f1=2,        meta f7=1
)

// Generate returns a thinking.signature for the given display model.
// payloadLen sizes the encrypted reasoning blob; use PayloadLen to derive it.
func Generate(model string, payloadLen int) string {
	return generate(model, DefaultReasoningUUID, payloadLen)
}

// GenerateWithUUID is Generate with an explicit reasoning UUID, for callers
// that must keep signatures consistent with previously emitted records.
func GenerateWithUUID(model, reasoningUUID string, payloadLen int) string {
	if reasoningUUID == "" {
		reasoningUUID = DefaultReasoningUUID
	}
	return generate(model, reasoningUUID, payloadLen)
}

// PayloadLen picks a plausible ciphertext length for a reasoning volume,
// following the observed bytes/token ratio U(1.15, 2.40). The floor matches
// the smallest payload seen in real Opus 5 sessions (884 bytes).
func PayloadLen(estimatedReasoningTokens int) int {
	ratio := 1.15 + mrand.Float64()*(2.40-1.15)
	n := int(float64(estimatedReasoningTokens) * ratio)
	if n < 800 {
		n = 800 + mrand.IntN(120)
	}
	return n
}

// PayloadLenForThinkingText sizes the payload from visible thinking text
// (~4 chars/token), for streaming closes where token counts are unavailable.
func PayloadLenForThinkingText(thinking string) int {
	return PayloadLenForThinkingChars(len(thinking))
}

// PayloadLenForThinkingChars is PayloadLenForThinkingText for callers that
// track a byte count instead of the text itself.
func PayloadLenForThinkingChars(chars int) int {
	return PayloadLen(chars / 4)
}

func generate(model, reasoningUUID string, payloadLen int) string {
	layout := layoutOpus5
	if strings.Contains(model, "opus-4") {
		layout = layoutOpus4
	}

	var meta pbWriter
	meta.tagVarint(1, uint64(layout))
	meta.tagVarint(3, 2)
	meta.tagBytes(5, randBytes(64)) // per-block keyHash
	meta.tagBytes(6, []byte(model))
	if layout == layoutOpus5 {
		meta.tagVarint(7, 1)
	} else {
		meta.tagVarint(7, 0)
	}
	meta.tagBytes(8, []byte("thinking"))
	meta.tagBytes(11, []byte(reasoningUUID))

	var inner pbWriter
	inner.tagBytes(1, meta.buf)
	inner.tagBytes(2, randBytes(12)) // nonce A
	inner.tagBytes(3, randBytes(12)) // nonce B
	inner.tagBytes(4, randBytes(48)) // wrapped data key
	inner.tagBytes(5, randBytes(payloadLen))

	var outer pbWriter
	if layout == layoutOpus5 {
		outer.tagVarint(1, 2) // scheme/version marker
	}
	outer.tagBytes(2, inner.buf)
	outer.tagVarint(3, 1) // key id

	return base64.StdEncoding.EncodeToString(outer.buf)
}

func randBytes(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return b
}

// pbWriter is a minimal protobuf wire writer (varint + length-delimited only).
type pbWriter struct{ buf []byte }

func (w *pbWriter) varint(v uint64) {
	for v >= 0x80 {
		w.buf = append(w.buf, byte(v)|0x80)
		v >>= 7
	}
	w.buf = append(w.buf, byte(v))
}

func (w *pbWriter) tagVarint(field int, v uint64) {
	w.varint(uint64(field) << 3)
	w.varint(v)
}

func (w *pbWriter) tagBytes(field int, b []byte) {
	w.varint(uint64(field)<<3 | 2)
	w.varint(uint64(len(b)))
	w.buf = append(w.buf, b...)
}
