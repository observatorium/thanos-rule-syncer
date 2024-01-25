package main

import (
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/cenkalti/backoff/v4"
)

// RetryableTransport wraps an http.RoundTripper and retries on failure.
type RetryableTransport struct {
	transport     http.RoundTripper
	backoffConfig *backoff.ExponentialBackOff
}

// RetryableTransportCfg is the configuration for a RetryableTransport.
type RetryableTransportCfg struct {
	Transport       http.RoundTripper
	InitialInterval time.Duration
	MaxInterval     time.Duration
	MaxElapsedTime  time.Duration
}

// NewRetryableTransport creates a new RetryableTransport.
func NewRetryableTransport(cfg *RetryableTransportCfg) *RetryableTransport {
	setIfNotZero := func(dst *time.Duration, src time.Duration) {
		if src != 0 {
			*dst = src
		}
	}

	backoffConfig := backoff.NewExponentialBackOff()
	setIfNotZero(&backoffConfig.InitialInterval, cfg.InitialInterval)
	setIfNotZero(&backoffConfig.MaxInterval, cfg.MaxInterval)
	setIfNotZero(&backoffConfig.MaxElapsedTime, cfg.MaxElapsedTime)

	return &RetryableTransport{
		transport:     cfg.Transport,
		backoffConfig: backoffConfig,
	}
}

func (r *RetryableTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var resp *http.Response
	var err error
	startTime := time.Now()

	operation := func() error {
		resp, err = r.transport.RoundTrip(req)
		if err != nil {
			if err, ok := err.(net.Error); ok && err.Timeout() {
				return err
			}
			return backoff.Permanent(err)
		}

		switch {
		case resp.StatusCode >= 500:
			resp.Body.Close()
			return fmt.Errorf("server error: %d %v", resp.StatusCode, resp.Status)
		case resp.StatusCode == http.StatusTooManyRequests:
			resp.Body.Close()
			if retryAfter := resp.Header.Get("Retry-After"); retryAfter != "" {
				if delay, err := time.ParseDuration(retryAfter); err == nil {
					if delay > r.backoffConfig.MaxElapsedTime || time.Since(startTime)+delay > r.backoffConfig.MaxElapsedTime {
						return backoff.Permanent(fmt.Errorf("retry-after delay is greater than max elapsed time: %v", delay))
					}

					time.Sleep(delay)
				}
			}
			return fmt.Errorf("rate limit reached: %d %v", resp.StatusCode, resp.Status)
		case resp.StatusCode >= 400:
			resp.Body.Close()
			return backoff.Permanent(fmt.Errorf("client error: %d %v", resp.StatusCode, resp.Status))
		}

		return nil
	}

	backoff.Retry(operation, r.backoffConfig)

	return resp, err
}
