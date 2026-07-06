package main

import (
	"regexp"
	"strings"
)

// ── Carrier auto-detection ─────────────────────────

type CarrierRule struct {
	Code     string
	Name     string
	Pattern  *regexp.Regexp
	Priority int
}

var carrierRules []CarrierRule

func init() {
	rules := []struct {
		code     string
		name     string
		pattern  string
		priority int
	}{
		{"SF", "顺丰速运", `^SF`, 0},
		{"JD", "京东物流", `^JD[A-Z]?`, 0},
		{"YTO", "圆通速递", `^YT\d`, 0},
		{"STO", "申通快递", `^(77|268|368|488|588|688|778)\d{10,}$`, 0},
		{"ZTO", "中通快递", `^(78|76|21|68|75|73|218|228|158|118)\d{9,}$`, 0},
		{"YUNDA", "韵达快递", `^(10|11|12|13|14|15|16|31|33|43)\d{11,}$`, 0},
		{"HTKY", "百世快递", `^(7[0-4])\d{11,}$`, 0},
		{"JTSD", "极兔速递", `^JT\d`, 0},
		{"EMS", "EMS", `^[A-Z]{2}\d{9}[A-Z]{2}$`, 0},
		{"YZPY", "邮政包裹", `^(99|98|10)\d{11}$`, 0},
		{"DBL", "德邦快递", `^(DPK|DPHE|62)\d`, 0},
		{"ZJS", "宅急送", `^[A-Z]{2}\d{8,}`, -1},
		{"FAST", "快捷快递", `^(AAA|ABB)\d`, 0},
		{"TTKDEX", "天天快递", `^(66|77|88)\d{10}$`, 0},
		{"GTO", "国通快递", `^(3(0[0356]|100))\d{8}$`, 0},
		{"UC", "优速快递", `^VIP\d`, 0},
		{"ANE", "安能物流", `^(AN|2[12]0)\d`, 0},
		{"CNSD", "菜鸟速递", `^CN\d`, 0},
	}

	for _, r := range rules {
		carrierRules = append(carrierRules, CarrierRule{
			Code:     r.code,
			Name:     r.name,
			Pattern:  regexp.MustCompile(r.pattern),
			Priority: r.priority,
		})
	}
}

func detectCarrier(mailNo string) string {
	if mailNo == "" {
		return ""
	}
	no := strings.TrimSpace(mailNo)
	for _, rule := range carrierRules {
		if rule.Pattern.MatchString(no) {
			return rule.Code
		}
	}
	return ""
}

func getCarrierName(code string) string {
	for _, rule := range carrierRules {
		if rule.Code == code {
			return rule.Name
		}
	}
	return ""
}

func getCarrierRules() []map[string]string {
	result := make([]map[string]string, len(carrierRules))
	for i, r := range carrierRules {
		result[i] = map[string]string{"code": r.Code, "name": r.Name}
	}
	return result
}
