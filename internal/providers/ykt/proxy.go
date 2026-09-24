package ykt

import (
	"bytes"
	"errors"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const authInspectionBytes = 64 << 10
const defaultMaxRequestBytes = 16 << 20

func isAuthFailure(status int, body []byte) bool {
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return true
	}
	return bodyLooksLoggedOut(body)
}

func bodyLooksLoggedOut(body []byte) bool {
	s := string(body)
	for _, marker := range []string{
		`"code":"2001"`, `"code": "2001"`, `"code":2001`, `"code": 2001`,
		"未登录", "登录已过期", "token失效", "token 失效", "请先登录",
	} {
		if strings.Contains(s, marker) {
			return true
		}
	}
	return false
}

var hopHeaders = map[string]bool{
	"connection": true, "keep-alive": true, "proxy-authenticate": true,
	"proxy-authorization": true, "proxy-connection": true, "te": true,
	"trailer": true, "trailers": true, "transfer-encoding": true,
	"upgrade": true,
}

var callerOnlyHeaders = map[string]bool{
	"authorization": true, "cookie": true, "x-api-key": true,
	"hsycf-api-key": true, "token": true, "host": true, "content-length": true,
	"accept-encoding": true, "forwarded": true, "x-forwarded-for": true,
	"x-forwarded-host": true, "x-forwarded-proto": true, "x-real-ip": true,
}

func connectionHeaders(h http.Header) map[string]bool {
	result := make(map[string]bool)
	for _, value := range h.Values("Connection") {
		for _, name := range strings.Split(value, ",") {
			name = strings.ToLower(strings.TrimSpace(name))
			if name != "" {
				result[name] = true
			}
		}
	}
	return result
}

func setProxyHeaders(req *http.Request) {
	refererPath := "/index"
	if req.URL.Path == "/api/basic/findDataAreaBoard" {
		refererPath = "/transactionDetail"
		req.Header.Set("Cache-Control", "no-cache")
		req.Header.Set("Pragma", "no-cache")
	}
	setBrowserHeaders(req, refererPath)
}

func (p *Provider) handleProxy(w http.ResponseWriter, r *http.Request) {
	limit := p.cfg.MaxRequestBytes
	if limit <= 0 {
		limit = defaultMaxRequestBytes
	}
	var requestBody []byte
	if r.Body != nil {
		var err error
		requestBody, err = io.ReadAll(io.LimitReader(r.Body, limit+1))
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		if int64(len(requestBody)) > limit {
			writeJSONError(w, http.StatusRequestEntityTooLarge, "request body too large")
			return
		}
	}

	token, err := p.ensureToken(false)
	if err != nil {
		if errors.Is(err, ErrMissingCredential) {
			writeJSONError(w, http.StatusServiceUnavailable, "missing credential")
		} else {
			writeJSONError(w, http.StatusServiceUnavailable, "login failed")
		}
		return
	}

	resp, err := p.proxyOnce(r, token, requestBody)
	if err != nil {
		log.Printf("[ERROR] proxy upstream request failed\n")
		writeJSONError(w, http.StatusBadGateway, "upstream error")
		return
	}
	prefix, err := readInspectionPrefix(resp)
	if err != nil {
		resp.Body.Close()
		writeJSONError(w, http.StatusBadGateway, "upstream error")
		return
	}
	if isAuthFailure(resp.StatusCode, prefix) {
		resp.Body.Close()
		newToken, loginErr := p.ensureToken(true)
		if loginErr != nil {
			log.Printf("[WARN] proxy re-login failed\n")
			writeJSONError(w, http.StatusServiceUnavailable, "login failed")
			return
		}
		resp, err = p.proxyOnce(r, newToken, requestBody)
		if err != nil {
			log.Printf("[ERROR] proxy retry failed\n")
			writeJSONError(w, http.StatusBadGateway, "upstream error")
			return
		}
		prefix, err = readInspectionPrefix(resp)
		if err != nil {
			resp.Body.Close()
			writeJSONError(w, http.StatusBadGateway, "upstream error")
			return
		}
	}
	defer resp.Body.Close()

	p.mu.Lock()
	p.served++
	p.mu.Unlock()

	omit := connectionHeaders(resp.Header)
	for k, vals := range resp.Header {
		lk := strings.ToLower(k)
		if hopHeaders[lk] || omit[lk] || lk == "content-length" || lk == "set-cookie" || lk == "token" {
			continue
		}
		for _, v := range vals {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	if _, err := w.Write(prefix); err != nil {
		return
	}
	if _, err := io.Copy(w, resp.Body); err != nil {
		log.Printf("[WARN] proxy response copy failed\n")
	}
}

func readInspectionPrefix(resp *http.Response) ([]byte, error) {
	return io.ReadAll(io.LimitReader(resp.Body, authInspectionBytes))
}

func (p *Provider) proxyOnce(r *http.Request, token string, bodyBytes []byte) (*http.Response, error) {
	target := p.cfg.BaseURL + r.URL.EscapedPath()
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	upReq, err := http.NewRequestWithContext(r.Context(), r.Method, target, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}
	omit := connectionHeaders(r.Header)
	for k, vals := range r.Header {
		lk := strings.ToLower(k)
		if hopHeaders[lk] || callerOnlyHeaders[lk] || strings.HasPrefix(lk, "x-forwarded-") || omit[lk] || (lk == "content-type" && len(bodyBytes) == 0) {
			continue
		}
		for _, v := range vals {
			upReq.Header.Add(k, v)
		}
	}
	setProxyHeaders(upReq)
	upReq.Header.Set("Token", token)
	return p.proxyClient.Do(upReq)
}

func newProxyClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) == 0 || len(via) >= 10 || !sameOrigin(req.URL, via[0].URL) {
				return http.ErrUseLastResponse
			}
			return nil
		},
		Transport: &http.Transport{
			Proxy:                 nil,
			MaxIdleConnsPerHost:   4,
			IdleConnTimeout:       30 * time.Second,
			ResponseHeaderTimeout: timeout,
		},
	}
}

func sameOrigin(a, b *url.URL) bool {
	return a.Scheme == b.Scheme && strings.EqualFold(a.Host, b.Host)
}
