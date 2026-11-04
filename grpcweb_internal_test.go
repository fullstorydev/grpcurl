package grpcurl

import (
	"testing"
	"time"

	"google.golang.org/grpc/codes"
)

func TestEncodeGRPCTimeout(t *testing.T) {
	testCases := []struct {
		d    time.Duration
		want string
	}{
		{-time.Second, "1n"},
		{0, "1n"},
		{time.Nanosecond, "1n"},
		{99999999 * time.Nanosecond, "99999999n"},
		{100 * time.Millisecond, "100000u"},
		{10 * time.Second, "10000000u"},
		{100 * time.Second, "100000m"},
		{100000*time.Second + time.Millisecond, "100001S"},
		{time.Duration(1<<63 - 1), "2562048H"},
	}
	for _, tc := range testCases {
		if got := encodeGRPCTimeout(tc.d); got != tc.want {
			t.Errorf("encodeGRPCTimeout(%v) = %q; want %q", tc.d, got, tc.want)
		}
	}
}

func TestDecodeGRPCMessage(t *testing.T) {
	testCases := map[string]string{
		"":                 "",
		"plain":            "plain",
		"a%20b":            "a b",
		"100%25":           "100%",
		"%E2%9C%93 done":   "\u2713 done",
		"bad %zz escape":   "bad %zz escape",
		"trailing %2":      "trailing %2",
		"%41%42%43":        "ABC",
		"mixed%0D%0Alines": "mixed\r\nlines",
	}
	for in, want := range testCases {
		if got := decodeGRPCMessage(in); got != want {
			t.Errorf("decodeGRPCMessage(%q) = %q; want %q", in, got, want)
		}
	}
}

func TestParseGRPCWebTrailers(t *testing.T) {
	md, err := parseGRPCWebTrailers([]byte("grpc-status: 7\r\nGrpc-Message: denied\r\nX-Multi: a\r\nx-multi: b\r\nx-raw-bin: AAEC\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	st := statusFromMetadata(md)
	if st.Code() != codes.PermissionDenied || st.Message() != "denied" {
		t.Errorf("wrong status: %v", st)
	}
	if v := md["x-multi"]; len(v) != 2 || v[0] != "a" || v[1] != "b" {
		t.Errorf("wrong multi-valued trailer: %v", v)
	}
	if v := md["x-raw-bin"]; len(v) != 1 || v[0] != "\x00\x01\x02" {
		t.Errorf("binary trailer not decoded: %q", v)
	}
	if _, ok := md["grpc-status"]; ok {
		t.Errorf("status keys should be removed from trailer metadata: %v", md)
	}

	if _, err := parseGRPCWebTrailers([]byte("no colon here\r\n")); err == nil {
		t.Error("expected error for malformed trailer line")
	}
	if st := statusFromMetadata(map[string][]string{}); st.Code() != codes.Internal {
		t.Errorf("missing grpc-status should be Internal; got %v", st)
	}
}

func TestGRPCWebBaseURL(t *testing.T) {
	testCases := []struct {
		target string
		tls    bool
		want   string
	}{
		{"localhost:8080", false, "http://localhost:8080"},
		{"example.com:443", true, "https://example.com:443"},
		{"example.com:443/api/", true, "https://example.com:443/api"},
		{"example.com/a/b", false, "http://example.com/a/b"},
		{"https://example.com/grpc/", false, "https://example.com/grpc"},
	}
	for _, tc := range testCases {
		got, err := grpcWebBaseURL(tc.target, tc.tls)
		if err != nil || got != tc.want {
			t.Errorf("grpcWebBaseURL(%q, %v) = %q, %v; want %q", tc.target, tc.tls, got, err, tc.want)
		}
	}
	if _, err := grpcWebBaseURL("/just/a/path", false); err == nil {
		t.Error("expected error for target without host")
	}
}
