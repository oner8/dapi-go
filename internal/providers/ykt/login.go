package ykt

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// LoginClient handles ykt login flow.
type LoginClient struct {
	baseURL    string
	account    string
	password   string
	httpClient *http.Client
}

// NewLoginClient creates a new login client.
func NewLoginClient(baseURL, account, password string, timeout time.Duration) *LoginClient {
	transport := &http.Transport{
		Proxy: nil,
	}
	return &LoginClient{
		baseURL:  baseURL,
		account:  account,
		password: password,
		httpClient: &http.Client{
			Timeout:   timeout,
			Transport: transport,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) == 0 || len(via) >= 10 || !sameOrigin(req.URL, via[0].URL) {
					return http.ErrUseLastResponse
				}
				return nil
			},
		},
	}
}

// Login performs the full two-step login and returns the token.
// Returns the token string and error (retries once on failure).
func (lc *LoginClient) Login() (string, error) {
	// Step 1: Get captcha
	captcha, err := lc.getCaptcha()
	if err != nil {
		log.Printf("[ERROR] captcha step failed: %v\n", err)
		return "", err
	}
	log.Printf("[DEBUG] fetched captcha\n")

	// Step 2: First login request (operatorCheckLogin)
	operatorMsg, areaID, err := lc.operatorCheckLogin(captcha)
	if err != nil {
		log.Printf("[ERROR] first login step failed\n")
		return "", err
	}
	log.Printf("[DEBUG] first login step succeeded\n")

	// Step 3: Second login request (operatorCheckIndex)
	token, err := lc.operatorCheckIndex(areaID, operatorMsg)
	if err != nil {
		log.Printf("[ERROR] second login step failed\n")
		return "", err
	}
	log.Printf("[DEBUG] operatorCheckIndex success, got token\n")

	return token, nil
}

// getCaptcha fetches the verification code.
func (lc *LoginClient) getCaptcha() (string, error) {
	endpoint := fmt.Sprintf("%s/api/login/findVerificationCode", lc.baseURL)
	req, err := http.NewRequest(http.MethodGet, endpoint+"?accountId="+url.QueryEscape(lc.account), nil)
	if err != nil {
		return "", fmt.Errorf("build captcha request")
	}
	req.Header.Set("Accept", "application/json, text/plain, */*")
	setBrowserHeaders(req, "/login")
	resp, err := lc.httpClient.Do(req)
	if err != nil {
		if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
			return "", fmt.Errorf("captcha request timed out")
		}
		return "", fmt.Errorf("captcha request failed (network or TLS error)")
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("captcha endpoint returned %d", resp.StatusCode)
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("captcha response is not valid JSON")
	}

	// Parse captcha - could be in data field as string or object
	data, ok := result["data"]
	if !ok {
		return "", fmt.Errorf("no data field in captcha response")
	}

	var captcha string
	switch v := data.(type) {
	case string:
		captcha = v
	case map[string]interface{}:
		// Try common field names
		for _, key := range []string{"verificationCode", "code", "value", "captcha"} {
			if c, ok := v[key].(string); ok {
				captcha = c
				break
			}
		}
	}

	if captcha == "" {
		return "", fmt.Errorf("could not extract captcha from response")
	}

	return captcha, nil
}

// operatorCheckLogin performs the first login step.
func (lc *LoginClient) operatorCheckLogin(captcha string) (operatorMsg, areaID string, err error) {
	endpoint := fmt.Sprintf("%s/api/login/operatorCheckLogin", lc.baseURL)

	// Prepare request body
	now := time.Now().UnixMilli()
	timestamp := strconv.FormatInt(now, 10)
	encryptData := lc.computeEncryptData(timestamp, captcha)

	encryptedPwd, err := EncryptPassword(lc.password)
	if err != nil {
		return "", "", fmt.Errorf("RSA encryption failed: %w", err)
	}

	body := map[string]interface{}{
		"accountId":        lc.account,
		"pwd":              encryptedPwd,
		"verificationCode": captcha,
		"type":             1,
		"timestamp":        timestamp,
		"encryptData":      encryptData,
	}

	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return "", "", fmt.Errorf("encode login request: %w", err)
	}
	req, err := http.NewRequest("POST", endpoint, bytes.NewReader(bodyJSON))
	if err != nil {
		return "", "", fmt.Errorf("build login request")
	}
	req.Header.Set("Content-Type", "application/json;charset=UTF-8")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	setBrowserHeaders(req, "/login")

	resp, err := lc.httpClient.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("first login request failed")
	}
	defer resp.Body.Close()

	bodyBytes, _ := ioutil.ReadAll(resp.Body)

	var result map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &result); err != nil {
		return "", "", err
	}

	// Check success
	if success, ok := result["success"].(bool); !ok || !success {
		msg, _ := result["message"].(string)
		_ = msg
		return "", "", fmt.Errorf("first login rejected by upstream")
	}

	// Extract data
	data, ok := result["data"].(map[string]interface{})
	if !ok {
		return "", "", fmt.Errorf("no data field in response")
	}

	// Get operator_msg
	opMsg, ok := data["operator_msg"].(string)
	if !ok {
		return "", "", fmt.Errorf("no operator_msg in response")
	}

	// Get area[0].id
	areas, ok := data["area"].([]interface{})
	if !ok || len(areas) == 0 {
		return "", "", fmt.Errorf("no area array in response")
	}

	areaObj, ok := areas[0].(map[string]interface{})
	if !ok {
		return "", "", fmt.Errorf("area[0] is not an object")
	}

	areaIdVal, ok := areaObj["id"]
	if !ok {
		return "", "", fmt.Errorf("no id in area[0]")
	}

	// Convert areaID to string (could be int or string)
	var aid string
	switch v := areaIdVal.(type) {
	case float64:
		aid = fmt.Sprintf("%.0f", v)
	case string:
		aid = v
	default:
		return "", "", fmt.Errorf("area id is not int or string")
	}

	return opMsg, aid, nil
}

// operatorCheckIndex performs the second login step.
func (lc *LoginClient) operatorCheckIndex(areaID, operatorMsg string) (string, error) {
	endpoint := fmt.Sprintf("%s/api/login/operatorCheckIndex", lc.baseURL)
	q := url.Values{}
	q.Set("areaId", areaID)
	q.Set("operator_msg", operatorMsg)

	req, err := http.NewRequest(http.MethodGet, endpoint+"?"+q.Encode(), nil)
	if err != nil {
		return "", fmt.Errorf("build second login request")
	}
	req.Header.Set("Accept", "application/json, text/plain, */*")
	setBrowserHeaders(req, "/login")
	resp, err := lc.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("second login request failed")
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}

	// Check success
	if success, ok := result["success"].(bool); !ok || !success {
		return "", fmt.Errorf("operatorCheckIndex returned success=false")
	}

	// Extract token from data.userInfo.token
	data, ok := result["data"].(map[string]interface{})
	if !ok {
		return "", fmt.Errorf("no data field in response")
	}

	userInfo, ok := data["userInfo"].(map[string]interface{})
	if !ok {
		return "", fmt.Errorf("no userInfo in data")
	}

	token, ok := userInfo["token"].(string)
	if !ok {
		return "", fmt.Errorf("no token in userInfo")
	}

	return token, nil
}

// computeEncryptData computes the encryptData field: md5(timestamp + accountId + verificationCode)
func (lc *LoginClient) computeEncryptData(timestamp, captcha string) string {
	data := timestamp + lc.account + captcha
	hash := md5.Sum([]byte(data))
	return hex.EncodeToString(hash[:])
}
