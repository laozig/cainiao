package main

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ── Cainiao API constants ──────────────────────────

const apiURL = "https://acs.m.taobao.com/h5/mtop.taobao.logisticstracedetailservice.queryalltrace/1.0/"
const userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/149.0.0.0 Safari/537.36"

var cainiaoHeaders = map[string]string{
	"Referer":    "https://page.cainiao.com/",
	"Origin":     "https://page.cainiao.com",
	"User-Agent": userAgent,
}

// ── Helpers ────────────────────────────────────────

func md5Hash(s string) string {
	h := md5.Sum([]byte(s))
	return hex.EncodeToString(h[:])
}

// strVal safely converts interface{} to string; nil → ""
// BUG FIX: fmt.Sprintf("%v", nil) produces "<nil>" string,
// but Node's `val || ''` naturally converts undefined → "".
func strVal(v interface{}) string {
	if v == nil {
		return ""
	}
	switch val := v.(type) {
	case string:
		return val
	case float64:
		if val == float64(int64(val)) {
			return fmt.Sprintf("%d", int64(val))
		}
		return fmt.Sprintf("%v", val)
	default:
		return fmt.Sprintf("%v", val)
	}
}

func buildData(mailNo, cpCode string) string {
	return toJSON(map[string]interface{}{
		"mailNo":                   mailNo,
		"orderCode":                "",
		"cpCode":                   cpCode,
		"appName":                  "GUOGUO",
		"actor":                    "RECEIVER",
		"isAccoutOut":              true,
		"isShowConsignDetail":      true,
		"ignoreInvalidNode":        true,
		"isUnique":                 true,
		"isStandard":               true,
		"isShowItem":               true,
		"isShowTemporalityService": true,
		"isShowCommonService":      true,
		"isStandardActionCode":     true,
		"isOrderByAction":          true,
		"isShowExpressMan":         true,
		"isShowProgressbar":        true,
		"isShowLastOneService":     true,
		"isShowServiceProvider":    true,
		"isShowDeliveryProgress":   true,
	})
}

func parseSetCookie(raw []string) map[string]string {
	cookies := map[string]string{}
	for _, h := range raw {
		parts := strings.SplitN(h, ";", 2)
		kv := strings.SplitN(parts[0], "=", 2)
		if len(kv) == 2 {
			cookies[strings.TrimSpace(kv[0])] = strings.TrimSpace(kv[1])
		}
	}
	return cookies
}

func fmtCookies(c map[string]string) string {
	parts := []string{}
	for k, v := range c {
		parts = append(parts, k+"="+v)
	}
	return strings.Join(parts, "; ")
}

// ── Proxy ──────────────────────────────────────────

func getProxy(apiURL string) (string, error) {
	if apiURL == "" {
		return "", nil
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(apiURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	raw := strings.TrimSpace(string(body))
	if raw == "" {
		return "", fmt.Errorf("empty proxy response")
	}

	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	return u.String(), nil
}

// ── Token fetch ────────────────────────────────────

type TokenResult struct {
	Token     string
	TkFull    string
	TkEnc     string
	CookieStr string
}

func fetchToken(proxyURL string, timeoutMs int) (*TokenResult, error) {
	t := strconv.FormatInt(time.Now().UnixMilli(), 10)
	ak := getAppKey()
	data := buildData("000000000000", "")
	sign := md5Hash("undefined&" + t + "&" + ak + "&" + data)

	params := url.Values{}
	params.Set("jsv", "2.6.1")
	params.Set("appKey", ak)
	params.Set("t", t)
	params.Set("sign", sign)
	params.Set("v", "1.0")
	params.Set("dataType", "json")
	params.Set("api", "mtop.taobao.logisticstracedetailservice.queryalltrace")
	params.Set("type", "originaljson")
	params.Set("data", data)

	fullURL := apiURL + "?" + params.Encode()

	req, err := http.NewRequest("GET", fullURL, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range cainiaoHeaders {
		req.Header.Set(k, v)
	}

	client := makeHTTPClient(proxyURL, timeoutMs)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	cookies := parseSetCookie(resp.Header["Set-Cookie"])
	tkFull := cookies["_m_h5_tk"]
	tkEnc := cookies["_m_h5_tk_enc"]

	token := ""
	if idx := strings.Index(tkFull, "_"); idx > 0 {
		token = tkFull[:idx]
	}

	return &TokenResult{
		Token:     token,
		TkFull:    tkFull,
		TkEnc:     tkEnc,
		CookieStr: fmtCookies(cookies),
	}, nil
}

// ── Query ─────────────────────────────────────────

func doQuery(mailNo, cpCode, token, tkFull, tkEnc, cookieStr, proxyURL string, timeoutMs int) (map[string]interface{}, error) {
	data := buildData(mailNo, cpCode)
	t := strconv.FormatInt(time.Now().UnixMilli(), 10)
	ak := getAppKey()
	sign := md5Hash(token + "&" + t + "&" + ak + "&" + data)

	params := url.Values{}
	params.Set("jsv", "2.6.1")
	params.Set("appKey", ak)
	params.Set("t", t)
	params.Set("sign", sign)
	params.Set("v", "1.0")
	params.Set("dataType", "json")
	params.Set("AntiCreep", "true")
	params.Set("api", "mtop.taobao.logisticstracedetailservice.queryalltrace")
	params.Set("encode", "1")
	params.Set("type", "originaljson")
	params.Set("c", tkFull+";"+tkEnc)
	params.Set("data", data)

	fullURL := apiURL + "?" + params.Encode()

	req, err := http.NewRequest("GET", fullURL, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range cainiaoHeaders {
		req.Header.Set(k, v)
	}
	req.Header.Set("Cookie", cookieStr)

	client := makeHTTPClient(proxyURL, timeoutMs)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var result map[string]interface{}
	if err := jsonUnmarshal(body, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// ── Parse result ───────────────────────────────────

func parseResult(data map[string]interface{}) *ParsedResult {
	ret, _ := data["ret"].([]interface{})
	success := false
	for _, r := range ret {
		if strings.Contains(strVal(r), "SUCCESS") {
			success = true
			break
		}
	}
	if !success {
		return nil
	}

	results, _ := data["data"].(map[string]interface{})["result"].([]interface{})
	if len(results) == 0 {
		return nil
	}

	pkg, _ := results[0].(map[string]interface{})
	cp, _ := pkg["cp"].(map[string]interface{})
	st, _ := pkg["packageStatus"].(map[string]interface{})
	tracesRaw, _ := pkg["fullTraceDetail"].([]interface{})

	statusText := strVal(st["newStatusDesc"])
	if statusText == "" {
		statusText = strVal(st["status"])
	}
	statusCodeText := strVal(st["newStatusCode"])
	if statusCodeText == "" {
		statusCodeText = strVal(st["statusCode"])
	}
	statusDescText := strVal(st["desc"])
	if statusDescText == "" {
		statusDescText = statusText
	}

	predict := ""
	if svcList, ok := pkg["temporalityServiceList"].([]interface{}); ok {
		for _, s := range svcList {
			if sm, ok := s.(map[string]interface{}); ok {
				if strVal(sm["serviceType"]) == "2" {
					predict = strVal(sm["desc"])
				}
			}
		}
	}

	var traces []TraceItem
	for _, tr := range tracesRaw {
		if tm, ok := tr.(map[string]interface{}); ok {
			desc := strVal(tm["standerdDesc"])
			if desc == "" {
				desc = strVal(tm["desc"])
			}
			traces = append(traces, TraceItem{
				Time: strVal(tm["time"]),
				Desc: desc,
			})
		}
	}

	lastTime := ""
	lastDesc := ""
	if len(traces) > 0 {
		lastTime = traces[len(traces)-1].Time
		lastDesc = traces[len(traces)-1].Desc
	}

	result := &ParsedResult{
		MailNo:     strVal(pkg["mailNo"]),
		CpCode:     strVal(cp["tpCode"]),
		CpName:     strVal(cp["tpName"]),
		Status:     statusText,
		StatusCode: statusCodeText,
		StatusDesc: statusDescText,
		Progress:   strVal(st["progressbar"]),
		From:       strVal(pkg["fetcherAddress"]),
		Current:    strVal(pkg["currentAddress"]),
		Predict:    predict,
		TraceCount: len(traces),
		LastTime:   lastTime,
		LastDesc:   lastDesc,
		Traces:     traces,
	}
	initTraces(result)
	return result
}

// ── Query with retry ───────────────────────────────

func queryWithRetry(mailNo, cpCode, proxyAPI string, timeoutMs int, maxRetries int, aborted func() bool) (map[string]interface{}, error) {
	var lastErr string

	for i := 0; i < maxRetries; i++ {
		if aborted != nil && aborted() {
			return nil, fmt.Errorf("已取消")
		}

		proxyURL := ""
		if proxyAPI != "" {
			var proxyErr error
			proxyURL, proxyErr = getProxy(proxyAPI)
			if proxyErr != nil || proxyURL == "" {
				if proxyErr != nil {
					lastErr = "取代理失败: " + proxyErr.Error()
				} else {
					lastErr = "取代理失败: 空代理"
				}
				if i < maxRetries-1 {
					time.Sleep(time.Duration(min(200*1<<uint(i), 3000)) * time.Millisecond)
				}
				continue
			}
		}

		tk, err := fetchToken(proxyURL, timeoutMs)
		if err != nil || tk.Token == "" {
			if err != nil {
				lastErr = "取token失败: " + err.Error()
			} else {
				lastErr = "取token失败: 空token"
			}
			if i < maxRetries-1 {
				time.Sleep(time.Duration(min(300*1<<uint(i), 5000)) * time.Millisecond)
			}
			continue
		}

		result, err := doQuery(mailNo, cpCode, tk.Token, tk.TkFull, tk.TkEnc, tk.CookieStr, proxyURL, timeoutMs)
		if err != nil {
			if aborted != nil && aborted() {
				return nil, fmt.Errorf("已取消")
			}
			lastErr = err.Error()
			if len(lastErr) > 120 {
				lastErr = lastErr[:120]
			}
			if i < maxRetries-1 {
				time.Sleep(time.Duration(min(500*1<<uint(i), 8000)) * time.Millisecond)
			}
			continue
		}

		ret, _ := result["ret"].([]interface{})
		hasSuccess := false
		hasTokenErr := false
		hasRgv := false
		for _, r := range ret {
			s := strVal(r)
			if strings.Contains(s, "SUCCESS") {
				hasSuccess = true
			}
			if strings.Contains(s, "TOKEN") {
				hasTokenErr = true
			}
			if strings.Contains(s, "RGV587") {
				hasRgv = true
			}
		}

		if hasSuccess {
			return result, nil
		}
		if hasTokenErr {
			lastErr = "token无效，已换代理重试"
			if i < maxRetries-1 {
				time.Sleep(time.Duration(min(300*1<<uint(i), 5000)) * time.Millisecond)
			}
			continue
		}
		if hasRgv {
			lastErr = "限流，已换代理重试"
			if i < maxRetries-1 {
				time.Sleep(time.Duration(min(500*1<<uint(i), 8000)) * time.Millisecond)
			}
			continue
		}

		return result, nil
	}

	return nil, fmt.Errorf("重试%d次失败: %s", maxRetries, lastErr)
}

// ── HTTP client with proxy ─────────────────────────

func makeHTTPClient(proxyURL string, timeoutMs int) *http.Client {
	transport := &http.Transport{}
	if proxyURL != "" {
		if pu, err := url.Parse(proxyURL); err == nil {
			transport.Proxy = http.ProxyURL(pu)
		}
	}
	if timeoutMs < 1000 {
		timeoutMs = 8000
	}
	return &http.Client{
		Timeout:   time.Duration(timeoutMs) * time.Millisecond,
		Transport: transport,
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
