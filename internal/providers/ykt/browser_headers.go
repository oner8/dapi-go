package ykt

import "net/http"

const browserUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/153.0.0.0 Safari/537.36"

// setBrowserHeaders keeps the browser metadata consistent with the configured
// upstream origin. Authentication and hop-by-hop headers are handled separately.
func setBrowserHeaders(req *http.Request, refererPath string) {
	if req.Header.Get("Accept") == "" {
		req.Header.Set("Accept", "*/*")
	}
	if req.Header.Get("Accept-Language") == "" {
		req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
	}
	req.Header.Set("User-Agent", browserUserAgent)
	req.Header.Set("Sec-Ch-Ua", `"Google Chrome";v="153", "Not_A Brand";v="8", "Chromium";v="153"`)
	req.Header.Set("Sec-Ch-Ua-Mobile", "?0")
	req.Header.Set("Sec-Ch-Ua-Platform", `"Windows"`)
	req.Header.Set("Sec-Fetch-Dest", "empty")
	req.Header.Set("Sec-Fetch-Mode", "cors")
	req.Header.Set("Sec-Fetch-Site", "same-origin")

	origin := req.URL.Scheme + "://" + req.URL.Host
	req.Header.Set("Referer", origin+refererPath)
	if req.Method == http.MethodGet || req.Method == http.MethodHead {
		req.Header.Del("Origin")
	} else {
		req.Header.Set("Origin", origin)
	}
}
