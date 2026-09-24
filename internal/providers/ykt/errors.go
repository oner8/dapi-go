package ykt

import "errors"

var (
	ErrMissingBaseURL     = errors.New("YKT_BASE_URL not set")
	ErrMissingAPIKey      = errors.New("YKT_API_KEY not set")
	ErrMissingCredential  = errors.New("missing YKT_ACCOUNT or YKT_PASSWORD")
	ErrLoginFailed        = errors.New("login failed")
	ErrCaptchaFetchFailed = errors.New("captcha fetch failed")
	ErrTokenParseFailed   = errors.New("token parse failed")
)
