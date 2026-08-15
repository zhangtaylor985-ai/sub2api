package sessiondelivery

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUnescapeJSONHTMLMatchesClientSerializer(t *testing.T) {
	cases := map[string]struct{ in, want string }{
		"reminder tag": {
			in:   `{"text":"\u003csystem-reminder\u003ehi\u003c/system-reminder\u003e"}`,
			want: `{"text":"<system-reminder>hi</system-reminder>"}`,
		},
		"ampersand": {
			in:   `{"url":"https://e.com?a=1\u00265b=2"}`,
			want: `{"url":"https://e.com?a=1&5b=2"}`,
		},
		"synthetic model": {
			in:   `{"model":"\u003csynthetic\u003e"}`,
			want: `{"model":"<synthetic>"}`,
		},
		"nothing to do": {
			in:   `{"a":1,"b":"plain"}`,
			want: `{"a":1,"b":"plain"}`,
		},
		"uppercase hex": {
			in:   `{"t":"\u003C\u003E"}`,
			want: `{"t":"<>"}`,
		},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			got := unescapeJSONHTML([]byte(testCase.in))
			require.Equal(t, testCase.want, string(got))
			// The rewrite must be semantically transparent.
			var before, after any
			require.NoError(t, json.Unmarshal([]byte(testCase.in), &before))
			require.NoError(t, json.Unmarshal(got, &after))
			require.Equal(t, before, after)
		})
	}
}

// A string whose literal text is a backslash followed by "u003c" is encoded as
// \\u003c. Rewriting that would change the value, so the backslash that
// introduces the escape has to be an unescaped one.
func TestUnescapeJSONHTMLPreservesEscapedBackslash(t *testing.T) {
	for _, document := range []string{
		`{"t":"\\u003c"}`,
		`{"t":"a\\u003cb"}`,
		`{"t":"\\\\u003c"}`,
		`{"t":"\\u003c\u003c"}`,
	} {
		got := unescapeJSONHTML([]byte(document))
		var before, after any
		require.NoError(t, json.Unmarshal([]byte(document), &before))
		require.NoError(t, json.Unmarshal(got, &after))
		require.Equal(t, before, after, "document %s became %s", document, got)
	}

	// Explicitly: the escaped backslash form is left untouched, while a real
	// escape in the same string is rewritten.
	require.Equal(t,
		`{"t":"\\u003c<"}`,
		string(unescapeJSONHTML([]byte(`{"t":"\\u003c\u003c"}`))),
	)
}

// The sequence must not be rewritten outside a string literal, where it cannot
// legally appear, and a quote inside an escape must not end the string early.
func TestUnescapeJSONHTMLTracksStringBoundaries(t *testing.T) {
	document := `{"a":"x\"\u003cy","b":"\u003c"}`
	got := unescapeJSONHTML([]byte(document))
	require.Equal(t, `{"a":"x\"<y","b":"<"}`, string(got))

	var before, after any
	require.NoError(t, json.Unmarshal([]byte(document), &before))
	require.NoError(t, json.Unmarshal(got, &after))
	require.Equal(t, before, after)
}

func TestUnescapeJSONHTMLHandlesEmptyAndTruncatedInput(t *testing.T) {
	require.Empty(t, unescapeJSONHTML(nil))
	for _, truncated := range []string{`{"t":"\`, `{"t":"\u00`, `{"t":"\u003`} {
		require.NotPanics(t, func() { unescapeJSONHTML([]byte(truncated)) })
	}
}
