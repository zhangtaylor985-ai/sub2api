package thinkingsig

import (
	"encoding/base64"
	"testing"
)

type field struct {
	num  int
	wire int
	vu   uint64
	data []byte
}

func consumeVarint(buf []byte) (uint64, int) {
	var r uint64
	var shift uint
	for i, b := range buf {
		r |= uint64(b&0x7f) << shift
		if b&0x80 == 0 {
			return r, i + 1
		}
		shift += 7
	}
	return r, len(buf)
}

func parseFields(t *testing.T, buf []byte) []field {
	t.Helper()
	var out []field
	pos := 0
	for pos < len(buf) {
		key, n := consumeVarint(buf[pos:])
		pos += n
		f := field{num: int(key >> 3), wire: int(key & 7)}
		switch f.wire {
		case 0:
			v, m := consumeVarint(buf[pos:])
			pos += m
			f.vu = v
		case 2:
			ln, m := consumeVarint(buf[pos:])
			pos += m
			if pos+int(ln) > len(buf) {
				t.Fatalf("field %d overruns buffer", f.num)
			}
			f.data = buf[pos : pos+int(ln)]
			pos += int(ln)
		default:
			t.Fatalf("unsupported wire type %d", f.wire)
		}
		out = append(out, f)
	}
	return out
}

func parseSignature(t *testing.T, b64 string) (outer, inner, meta []field) {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		t.Fatalf("signature is not valid base64: %v", err)
	}
	outer = parseFields(t, raw)
	if len(outer) < 2 || outer[len(outer)-2].num != 2 || outer[len(outer)-1].num != 3 {
		t.Fatalf("bad outer layout: %+v", outer)
	}
	inner = parseFields(t, outer[len(outer)-2].data)
	if len(inner) != 5 {
		t.Fatalf("bad inner layout: %+v", inner)
	}
	for i, want := range []int{1, 2, 3, 4, 5} {
		if inner[i].num != want || inner[i].wire != 2 {
			t.Fatalf("inner field %d: got num=%d wire=%d", i, inner[i].num, inner[i].wire)
		}
	}
	meta = parseFields(t, inner[0].data)
	if len(meta) != 7 {
		t.Fatalf("bad meta layout: %+v", meta)
	}
	return outer, inner, meta
}

func TestGenerateOpus5Layout(t *testing.T) {
	sig := Generate("claude-opus-5", 1480)
	outer, inner, meta := parseSignature(t, sig)

	// outer: f1 varint=2 (scheme), f2 envelope, f3 varint=1
	if len(outer) != 3 || outer[0].wire != 0 || outer[0].vu != 2 {
		t.Fatalf("opus-5 outer must start with scheme varint 2: %+v", outer[0])
	}
	if outer[2].vu != 1 {
		t.Fatalf("outer key id = %d, want 1", outer[2].vu)
	}

	// meta constants measured from real Opus 5 signatures
	if meta[0].vu != 16 || meta[1].vu != 2 || meta[4].vu != 1 {
		t.Fatalf("meta consts = %d/%d/%d, want 16/2/1", meta[0].vu, meta[1].vu, meta[4].vu)
	}
	if got := len(meta[2].data); got != 64 {
		t.Fatalf("keyHash len = %d, want 64", got)
	}
	if got := string(meta[3].data); got != "claude-opus-5" {
		t.Fatalf("model = %q", got)
	}
	if got := string(meta[5].data); got != "thinking" {
		t.Fatalf("tag = %q", got)
	}
	if got := string(meta[6].data); got != DefaultReasoningUUID {
		t.Fatalf("reasoning uuid = %q, want %q", got, DefaultReasoningUUID)
	}

	// header byte length must be exactly 135 for a 13-char model name
	if got := len(inner[0].data); got != 135 {
		t.Fatalf("meta len = %d, want 135", got)
	}
	if len(inner[1].data) != 12 || len(inner[2].data) != 12 || len(inner[3].data) != 48 {
		t.Fatalf("nonce/wrappedKey lens = %d/%d/%d, want 12/12/48",
			len(inner[1].data), len(inner[2].data), len(inner[3].data))
	}
	if got := len(inner[4].data); got != 1480 {
		t.Fatalf("payload len = %d, want 1480", got)
	}
}

func TestGenerateOpus4Layout(t *testing.T) {
	sig := Generate("claude-opus-4-8", 900)
	outer, inner, meta := parseSignature(t, sig)

	// opus-4.x has no outer scheme varint: outer = [f2 envelope, f3 keyId]
	if len(outer) != 2 || outer[0].num != 2 || outer[1].vu != 1 {
		t.Fatalf("opus-4 outer layout wrong: %+v", outer)
	}
	if meta[0].vu != 15 || meta[4].vu != 0 {
		t.Fatalf("opus-4 meta consts = %d/f7=%d, want 15/0", meta[0].vu, meta[4].vu)
	}
	if got := string(meta[3].data); got != "claude-opus-4-8" {
		t.Fatalf("model = %q", got)
	}
	// 15-char model => meta len 137
	if got := len(inner[0].data); got != 137 {
		t.Fatalf("meta len = %d, want 137", got)
	}
}

func TestGenerateUniqueness(t *testing.T) {
	a := Generate("claude-opus-5", 800)
	b := Generate("claude-opus-5", 800)
	if a == b {
		t.Fatal("two signatures for the same inputs must differ (fresh keyHash/nonces)")
	}
}

func TestGenerateWithUUIDOverride(t *testing.T) {
	const custom = "11111111-2222-4333-8444-555555555555"
	_, _, meta := parseSignature(t, GenerateWithUUID("claude-opus-5", custom, 300))
	if got := string(meta[6].data); got != custom {
		t.Fatalf("uuid = %q, want %q", got, custom)
	}
}

func TestPayloadLen(t *testing.T) {
	for i := 0; i < 1000; i++ {
		got := PayloadLen(1000)
		if got < 1150 || got > 2400 {
			t.Fatalf("PayloadLen(1000) = %d outside U(1.15,2.40)*1000", got)
		}
	}
	if got := PayloadLen(0); got < 800 || got >= 920 {
		t.Fatalf("PayloadLen(0) = %d outside floor band [800,920)", got)
	}
}
