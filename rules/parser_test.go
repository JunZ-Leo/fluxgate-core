package rules

import (
	"net/netip"
	"runtime"
	"testing"

	C "github.com/metacubex/mihomo/constant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseRuleCompatibility(t *testing.T) {
	tests := []struct {
		name     string
		ruleType string
		payload  string
		target   string
		params   []string
		wantType C.RuleType
		wantData string
	}{
		{
			name:     "domain normalized",
			ruleType: "DOMAIN",
			payload:  "Example.COM",
			target:   "Proxy",
			wantType: C.Domain,
			wantData: "example.com",
		},
		{
			name:     "match without payload",
			ruleType: "MATCH",
			target:   "DIRECT",
			wantType: C.MATCH,
		},
		{
			name:     "destination cidr",
			ruleType: "IP-CIDR",
			payload:  "10.0.0.0/8",
			target:   "Proxy",
			params:   []string{"no-resolve"},
			wantType: C.IPCIDR,
			wantData: "10.0.0.0/8",
		},
		{
			name:     "source cidr alias",
			ruleType: "SRC-IP-CIDR",
			payload:  "192.168.0.0/16",
			target:   "DIRECT",
			wantType: C.SrcIPCIDR,
			wantData: "192.168.0.0/16",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rule, err := ParseRule(tt.ruleType, tt.payload, tt.target, tt.params, nil)
			require.NoError(t, err)
			assert.Equal(t, tt.wantType, rule.RuleType())
			assert.Equal(t, tt.target, rule.Adapter())
			assert.Equal(t, tt.wantData, rule.Payload())
		})
	}
}

func TestParseRuleSourceParamCompatibility(t *testing.T) {
	rule, err := ParseRule("IP-CIDR", "10.0.0.0/8", "Proxy", []string{"src"}, nil)
	require.NoError(t, err)
	assert.Equal(t, C.SrcIPCIDR, rule.RuleType())

	resolveCalled := false
	matched, adapter := rule.Match(&C.Metadata{
		SrcIP: netip.MustParseAddr("10.1.2.3"),
		DstIP: netip.MustParseAddr("203.0.113.1"),
	}, C.RuleMatchHelper{
		ResolveIP: func() {
			resolveCalled = true
		},
	})
	assert.True(t, matched)
	assert.Equal(t, "Proxy", adapter)
	assert.False(t, resolveCalled)
}

func TestParseRuleErrorReturnsNilInterface(t *testing.T) {
	tests := []struct {
		ruleType string
		payload  string
		wantErr  string
	}{
		{"DOMAIN", "", "missing subsequent parameters: DOMAIN"},
		{"UNKNOWN", "value", "unsupported rule type: UNKNOWN"},
		{"NETWORK", "SCTP", "unsupported network type, only TCP/UDP"},
		{"DOMAIN-REGEX", "[", ""},
		{"PROCESS-NAME-REGEX", "[", ""},
		{"PROCESS-PATH-REGEX", "[", ""},
		{"SRC-PORT", "invalid", ""},
		{"DST-PORT", "invalid", ""},
		{"IN-PORT", "invalid", ""},
		{"DSCP", "64", "DSCP couldn't be negative or exceed 63"},
		{"UID", "invalid", ""},
		{"IN-TYPE", "unknown", "unknown type: UNKNOWN"},
		{"IN-TYPE", "HTTP/", "in type couldn't be empty"},
		{"IN-USER", "user/", "in user couldn't be empty"},
		{"IN-NAME", "listener/", "in name couldn't be empty"},
		{"REMATCH-NAME", "proxy/", "rematch name couldn't be empty"},
		{"IP-CIDR", "invalid", "payloadRule error"},
		{"NOT", "invalid", "payload format error"},
	}

	for _, tt := range tests {
		t.Run(tt.ruleType, func(t *testing.T) {
			rule, err := ParseRule(tt.ruleType, tt.payload, "DIRECT", nil, nil)
			require.Error(t, err)
			if tt.wantErr != "" {
				require.EqualError(t, err, tt.wantErr)
			}
			// Reflection-based Nil assertions also accept a typed nil interface.
			if rule != nil {
				t.Fatalf("constructor error returned a non-nil C.Rule (%T): %v", rule, err)
			}
		})
	}
}

func TestParseSimpleRulesCompatibility(t *testing.T) {
	tests := []struct {
		ruleType    string
		payload     string
		wantType    C.RuleType
		wantPayload string
	}{
		{"DOMAIN", "EXAMPLE.COM", C.Domain, "example.com"},
		{"DOMAIN-SUFFIX", "EXAMPLE.COM", C.DomainSuffix, "example.com"},
		{"DOMAIN-KEYWORD", "EXAMPLE", C.DomainKeyword, "example"},
		{"DOMAIN-REGEX", `^EXAMPLE\.COM$`, C.DomainRegex, `^EXAMPLE\.COM$`},
		{"DOMAIN-WILDCARD", "*.COM", C.DomainWildcard, "*.com"},
		{"SRC-PORT", "1024-65535", C.SrcPort, "1024-65535"},
		{"DST-PORT", "443", C.DstPort, "443"},
		{"IN-PORT", "7890", C.InPort, "7890"},
		{"DSCP", "46", C.DSCP, "46"},
		{"PROCESS-NAME", "BROWSER", C.ProcessName, "BROWSER"},
		{"PROCESS-PATH", "/APP/BROWSER", C.ProcessPath, "/APP/BROWSER"},
		{"PROCESS-NAME-REGEX", "^BROWSER$", C.ProcessNameRegex, "^BROWSER$"},
		{"PROCESS-PATH-REGEX", "^/APP/BROWSER$", C.ProcessPathRegex, "^/APP/BROWSER$"},
		{"PROCESS-NAME-WILDCARD", "BROW*", C.ProcessNameWildcard, "BROW*"},
		{"PROCESS-PATH-WILDCARD", "/APP/*", C.ProcessPathWildcard, "/APP/*"},
		{"NETWORK", "tcp", C.Network, "tcp"},
		{"UID", "1000", C.Uid, "1000"},
		{"IN-TYPE", "http/socks", C.InType, "HTTP/SOCKS"},
		{"IN-USER", "alice / bob", C.InUser, "alice / bob"},
		{"IN-NAME", "mixed / socks", C.InName, "mixed / socks"},
		{"REMATCH-NAME", "group / node", C.RematchName, "group / node"},
		{"MATCH", "", C.MATCH, ""},
	}
	metadata := C.Metadata{
		Host:        "example.com",
		SrcPort:     40000,
		DstPort:     443,
		InPort:      7890,
		DSCP:        46,
		Process:     "browser",
		ProcessPath: "/app/browser",
		NetWork:     C.TCP,
		Type:        C.SOCKS5,
		InUser:      "bob",
		InName:      "socks",
		RematchName: "node",
		Uid:         1000,
	}
	for _, tt := range tests {
		t.Run(tt.ruleType, func(t *testing.T) {
			rule, err := ParseRule(tt.ruleType, tt.payload, "Proxy", nil, nil)
			if tt.ruleType == "UID" && runtime.GOOS != "linux" && runtime.GOOS != "android" {
				require.EqualError(t, err, "uid rule not support this platform")
				require.True(t, rule == nil, "unsupported platforms must return a nil interface")
				return
			}
			require.NoError(t, err)
			require.NotNil(t, rule)
			assert.Equal(t, tt.wantType, rule.RuleType())
			assert.Equal(t, tt.wantPayload, rule.Payload())
			assert.Equal(t, "Proxy", rule.Adapter())
			assert.Empty(t, rule.ProviderNames())
			matched, target := rule.Match(&metadata, C.RuleMatchHelper{})
			assert.True(t, matched)
			assert.Equal(t, "Proxy", target)
		})
	}
}
