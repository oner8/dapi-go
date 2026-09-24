package ykt

import (
	"log"
	"sync"
	"time"
)

// refresher keeps the token valid: it re-logins proactively before expiry
// (background loop) so requests normally always hit a fresh token.
type refresher struct {
	p *Provider

	mu        sync.Mutex
	lastStart time.Time // start time of the last login attempt, for retry backoff
	lastFail  bool
}

const (
	// minRetryInterval prevents hammering the upstream when credentials or
	// the upstream are broken: at most one login attempt per interval.
	minRetryInterval = 60 * time.Second
	// maxWait caps how long the background loop sleeps between checks.
	maxWait = 60 * time.Second
	// soonWake is the short delay used when a token needs attention now.
	soonWake = 5 * time.Second
)

func newRefresher(p *Provider) *refresher {
	return &refresher{p: p}
}

// Start runs the background refresh loop until stop is closed.
func (r *refresher) Start(stop <-chan struct{}) {
	go func() {
		// Refresh shortly after start so a stale/absent token is fixed
		// without waiting for a request.
		timer := time.NewTimer(soonWake)
		defer timer.Stop()
		for {
			select {
			case <-stop:
				return
			case <-timer.C:
			}
			r.refreshIfNeeded()
			timer.Reset(r.nextDelay())
		}
	}()
}

// beginAttempt claims permission to run one login attempt. It enforces the
// retry backoff and returns false if another attempt ran recently.
func (r *refresher) beginAttempt() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	if !r.lastStart.IsZero() && now.Sub(r.lastStart) < minRetryInterval {
		return false
	}
	r.lastStart = now
	r.lastFail = false
	return true
}

// endAttempt records the outcome of the last attempt.
func (r *refresher) endAttempt(failed bool) {
	r.mu.Lock()
	r.lastFail = failed
	r.mu.Unlock()
}

// nextDelay computes how long to wait before the next refresh check.
func (r *refresher) nextDelay() time.Duration {
	r.mu.Lock()
	failedRecently := r.lastFail
	startedRecently := !r.lastStart.IsZero() && time.Since(r.lastStart) < minRetryInterval
	r.mu.Unlock()

	cache, hasToken := r.p.session.Snapshot()
	if !hasToken {
		// No token: if we just tried (and failed), back off; otherwise retry soon.
		if failedRecently || startedRecently {
			return minRetryInterval
		}
		return soonWake
	}

	remaining := cache.ExpiresAt - time.Now().Unix()
	margin := r.p.session.refreshMargin
	if remaining <= margin {
		return soonWake
	}
	// Wake up shortly after the margin boundary, checking at least every maxWait.
	wait := time.Duration(remaining-margin-5) * time.Second
	if wait < soonWake {
		wait = soonWake
	}
	if wait > maxWait {
		wait = maxWait
	}
	return wait
}

// refreshIfNeeded re-logins if the token is missing or expiring soon.
func (r *refresher) refreshIfNeeded() {
	p := r.p
	cache, hasToken := p.session.Snapshot()
	needsRefresh := !hasToken || IsTokenExpiringSoon(cache.ExpiresAt, p.session.refreshMargin)

	if !needsRefresh {
		return
	}
	if !r.beginAttempt() {
		return
	}

	if p.cfg.Account == "" || p.cfg.Password == "" {
		log.Printf("[ERROR] token refresh skipped: missing YKT_ACCOUNT or YKT_PASSWORD\n")
		r.endAttempt(true)
		return
	}

	log.Printf("[INFO] refreshing token (missing or expiring soon)\n")
	p.loginMu.Lock()
	// A request-path fallback may have refreshed the token while this
	// goroutine was waiting for the login slot.
	latest, hasLatest := p.session.Snapshot()
	stillNeedsRefresh := !hasLatest || IsTokenExpiringSoon(latest.ExpiresAt, p.session.refreshMargin)
	if !stillNeedsRefresh {
		p.loginMu.Unlock()
		r.endAttempt(false)
		return
	}

	_, err := p.loginAndStoreOnce()
	p.loginMu.Unlock()
	if err != nil {
		log.Printf("[ERROR] token refresh failed\n")
		r.endAttempt(true)
		return
	}

	r.endAttempt(false)
	log.Printf("[INFO] token refreshed successfully\n")
}
