package rules

import (
	"fmt"

	C "github.com/metacubex/mihomo/constant"
	RC "github.com/metacubex/mihomo/rules/common"
	"github.com/metacubex/mihomo/rules/logic"
	RP "github.com/metacubex/mihomo/rules/provider"
)

func ParseRule(tp, payload, target string, params []string, subRules map[string][]C.Rule) (parsed C.Rule, parseErr error) {
	if tp != "MATCH" && payload == "" { // only MATCH allowed doesn't contain payload
		return nil, fmt.Errorf("missing subsequent parameters: %s", tp)
	}

	if parsed, handled, err := parseSimpleRule(tp, payload, target); handled {
		if err != nil {
			return nil, err
		}
		return parsed, nil
	}

	switch tp {
	case "GEOSITE":
		parsed, parseErr = RC.NewGEOSITE(payload, target)
	case "GEOIP":
		isSrc, noResolve := RC.ParseParams(params)
		parsed, parseErr = RC.NewGEOIP(payload, target, isSrc, noResolve)
	case "SRC-GEOIP":
		parsed, parseErr = RC.NewGEOIP(payload, target, true, true)
	case "IP-ASN":
		isSrc, noResolve := RC.ParseParams(params)
		parsed, parseErr = RC.NewIPASN(payload, target, isSrc, noResolve)
	case "SRC-IP-ASN":
		parsed, parseErr = RC.NewIPASN(payload, target, true, true)
	case "IP-CIDR", "IP-CIDR6":
		isSrc, noResolve := RC.ParseParams(params)
		parsed, parseErr = RC.NewIPCIDR(payload, target, RC.WithIPCIDRSourceIP(isSrc), RC.WithIPCIDRNoResolve(noResolve))
	case "SRC-IP-CIDR":
		parsed, parseErr = RC.NewIPCIDR(payload, target, RC.WithIPCIDRSourceIP(true), RC.WithIPCIDRNoResolve(true))
	case "IP-SUFFIX":
		isSrc, noResolve := RC.ParseParams(params)
		parsed, parseErr = RC.NewIPSuffix(payload, target, isSrc, noResolve)
	case "SRC-IP-SUFFIX":
		parsed, parseErr = RC.NewIPSuffix(payload, target, true, true)
	case "SUB-RULE":
		parsed, parseErr = logic.NewSubRule(payload, target, subRules, ParseRule)
	case "AND":
		parsed, parseErr = logic.NewAND(payload, target, ParseRule)
	case "OR":
		parsed, parseErr = logic.NewOR(payload, target, ParseRule)
	case "NOT":
		parsed, parseErr = logic.NewNOT(payload, target, ParseRule)
	case "RULE-SET":
		isSrc, noResolve := RC.ParseParams(params)
		parsed, parseErr = RP.NewRuleSet(payload, target, isSrc, noResolve)
	default:
		parseErr = fmt.Errorf("unsupported rule type: %s", tp)
	}

	if parseErr != nil {
		return nil, parseErr
	}

	return
}

var _ RC.ParseRuleFunc = ParseRule
