package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestRetryableTransport(t *testing.T) {
	var callsCount int

	testCases := map[string]struct {
		setupServer      func() *httptest.Server
		transportCfg     *RetryableTransportCfg
		expectRetries    bool
		expectedError    bool
		expectedRespCode int
	}{
		"Success": {
			setupServer: func() *httptest.Server {
				return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					callsCount++
					w.WriteHeader(http.StatusOK)
				}))
			},
			transportCfg:     &RetryableTransportCfg{},
			expectedRespCode: http.StatusOK,
			expectRetries:    false,
		},
		"Server Error": {
			setupServer: func() *httptest.Server {
				return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					callsCount++
					w.WriteHeader(http.StatusInternalServerError)
				}))
			},
			transportCfg: &RetryableTransportCfg{
				InitialInterval: 50 * time.Millisecond,
				MaxInterval:     100 * time.Millisecond,
				MaxElapsedTime:  200 * time.Millisecond,
			},
			expectedRespCode: http.StatusInternalServerError,
			expectRetries:    true,
		},
		"Clientside Error": {
			setupServer: func() *httptest.Server {
				return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					callsCount++
					w.WriteHeader(http.StatusNotFound)
				}))
			},
			transportCfg:     &RetryableTransportCfg{},
			expectedRespCode: http.StatusNotFound,
			expectRetries:    false,
		},
		"Rate Limiting": {
			setupServer: func() *httptest.Server {
				return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					callsCount++
					w.Header().Set("Retry-After", "100ms")
					w.WriteHeader(http.StatusTooManyRequests)
				}))
			},
			transportCfg: &RetryableTransportCfg{
				InitialInterval: 50 * time.Millisecond,
				MaxInterval:     100 * time.Millisecond,
				MaxElapsedTime:  200 * time.Millisecond,
			},
			expectedRespCode: http.StatusTooManyRequests,
			expectRetries:    true,
		},
		"Rate Limiting with excessive Retry-After": {
			setupServer: func() *httptest.Server {
				return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					callsCount++
					w.Header().Set("Retry-After", "1h")
					w.WriteHeader(http.StatusTooManyRequests)
				}))
			},
			transportCfg: &RetryableTransportCfg{
				InitialInterval: 50 * time.Millisecond,
				MaxInterval:     100 * time.Millisecond,
				MaxElapsedTime:  200 * time.Millisecond,
			},
			expectedRespCode: http.StatusTooManyRequests,
			expectRetries:    false,
		},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			callsCount = 0
			server := tc.setupServer()
			defer server.Close()

			tc.transportCfg.Transport = server.Client().Transport
			transport := NewRetryableTransport(tc.transportCfg)

			req, err := http.NewRequest("GET", server.URL, nil)
			assert.NoError(t, err)

			resp, err := transport.RoundTrip(req)
			if tc.expectRetries {
				assert.True(t, callsCount > 1)
			}
			if tc.expectedError {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			assert.NotNil(t, resp)
			assert.Equal(t, tc.expectedRespCode, resp.StatusCode)
		})
	}
}
