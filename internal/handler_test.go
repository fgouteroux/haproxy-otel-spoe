package internal

import (
	"net"
	"testing"

	"go.opentelemetry.io/otel/attribute"
)

// ---------------------------------------------------------------------------
// parseCustomAttrs
// ---------------------------------------------------------------------------

func TestParseCustomAttrs(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want []attribute.KeyValue
	}{
		{
			name: "empty string",
			raw:  "",
			want: nil,
		},
		{
			name: "single pair",
			raw:  "env=production",
			want: []attribute.KeyValue{attribute.String("env", "production")},
		},
		{
			name: "multiple pairs",
			raw:  "env=production;datacenter=eu-west-1;team=ops",
			want: []attribute.KeyValue{
				attribute.String("env", "production"),
				attribute.String("datacenter", "eu-west-1"),
				attribute.String("team", "ops"),
			},
		},
		{
			name: "trailing semicolon",
			raw:  "env=production;",
			want: []attribute.KeyValue{attribute.String("env", "production")},
		},
		{
			name: "value with spaces trimmed",
			raw:  "env = staging",
			want: []attribute.KeyValue{attribute.String("env", "staging")},
		},
		{
			name: "pair without equals skipped",
			raw:  "badpair;env=prod",
			want: []attribute.KeyValue{attribute.String("env", "prod")},
		},
		{
			name: "empty key skipped",
			raw:  "=value;env=prod",
			want: []attribute.KeyValue{attribute.String("env", "prod")},
		},
		{
			name: "value may be empty",
			raw:  "env=",
			want: []attribute.KeyValue{attribute.String("env", "")},
		},
		{
			name: "value contains equals sign",
			raw:  "label=foo=bar",
			want: []attribute.KeyValue{attribute.String("label", "foo=bar")},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseCustomAttrs(tt.raw)

			if len(got) != len(tt.want) {
				t.Fatalf("got %d attrs; want %d\ngot:  %v\nwant: %v",
					len(got), len(tt.want), got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("attr[%d]: got %v; want %v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// kvString
// ---------------------------------------------------------------------------

func TestKvString(t *testing.T) {
	tests := []struct {
		name  string
		store map[string]any
		key   string
		want  string
	}{
		{
			name:  "existing string key",
			store: map[string]any{"method": "GET"},
			key:   "method",
			want:  "GET",
		},
		{
			name:  "missing key returns empty string",
			store: map[string]any{},
			key:   "method",
			want:  "",
		},
		{
			name:  "non-string value returns empty string",
			store: map[string]any{"port": 80},
			key:   "port",
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			get := func(k string) (any, bool) { v, ok := tt.store[k]; return v, ok }
			got := kvString(get, tt.key)
			if got != tt.want {
				t.Errorf("kvString(%q) = %q; want %q", tt.key, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// kvIP
// ---------------------------------------------------------------------------

func TestKvIP(t *testing.T) {
	tests := []struct {
		name  string
		store map[string]any
		key   string
		want  string
	}{
		{
			name:  "IPv4 address",
			store: map[string]any{"src": net.ParseIP("192.168.1.1")},
			key:   "src",
			want:  "192.168.1.1",
		},
		{
			name:  "IPv6 address",
			store: map[string]any{"src": net.ParseIP("::1")},
			key:   "src",
			want:  "::1",
		},
		{
			name:  "missing key returns empty string",
			store: map[string]any{},
			key:   "src",
			want:  "",
		},
		{
			name:  "non-IP value returns empty string",
			store: map[string]any{"src": "not-an-ip"},
			key:   "src",
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			get := func(k string) (any, bool) { v, ok := tt.store[k]; return v, ok }
			got := kvIP(get, tt.key)
			if got != tt.want {
				t.Errorf("kvIP(%q) = %q; want %q", tt.key, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// kvInt
// ---------------------------------------------------------------------------

func TestKvInt(t *testing.T) {
	tests := []struct {
		name  string
		store map[string]any
		key   string
		want  int
	}{
		{
			name:  "int value",
			store: map[string]any{"port": int(8080)},
			key:   "port",
			want:  8080,
		},
		{
			name:  "int32 value",
			store: map[string]any{"port": int32(443)},
			key:   "port",
			want:  443,
		},
		{
			name:  "int64 value",
			store: map[string]any{"port": int64(9090)},
			key:   "port",
			want:  9090,
		},
		{
			name:  "uint32 value",
			store: map[string]any{"port": uint32(80)},
			key:   "port",
			want:  80,
		},
		{
			name:  "missing key returns zero",
			store: map[string]any{},
			key:   "port",
			want:  0,
		},
		{
			name:  "non-numeric value returns zero",
			store: map[string]any{"port": "8080"},
			key:   "port",
			want:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			get := func(k string) (any, bool) { v, ok := tt.store[k]; return v, ok }
			got := kvInt(get, tt.key)
			if got != tt.want {
				t.Errorf("kvInt(%q) = %d; want %d", tt.key, got, tt.want)
			}
		})
	}
}
