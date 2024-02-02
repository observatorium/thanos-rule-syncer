package main_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	trs "github.com/observatorium/thanos-rule-syncer"
	"github.com/prometheus/prometheus/model/rulefmt"
	"github.com/stretchr/testify/assert"
)

var ruleGroups = `
groups:
- name: test
  rules:
  - alert: TestAlert
    expr: vector(1)
    for: 1m
- name: test2
  rules:
  - record: TestRecord2
    expr: vector(1)
`

func TestRulesObjtoreFetcher(t *testing.T) {
	var callsCount int64

	testCases := map[string]struct {
		tenants        []string
		responseBody   string
		responseStatus int
		ctxCancelled   bool

		expectErr    bool
		expectCalls  int
		expectGroups int
	}{
		"rule groups are aggregated": {
			tenants:        []string{"tenant1", "tenant2"},
			responseBody:   ruleGroups,
			responseStatus: http.StatusOK,
			expectCalls:    2,
			expectGroups:   4,
		},
		"first error returns": {
			tenants:        []string{"tenant1", "tenant2"},
			responseBody:   "internal server error",
			responseStatus: http.StatusInternalServerError,
			expectErr:      true,
			expectCalls:    1,
		},
		"empty response returns without error": {
			tenants:        []string{"tenant1"},
			responseBody:   "",
			responseStatus: http.StatusOK,
			expectCalls:    1,
			expectGroups:   0,
		},
		"timeout returns early": {
			tenants:        []string{"tenant1", "tenant2"},
			responseBody:   ruleGroups,
			responseStatus: http.StatusOK,
			ctxCancelled:   true,
			expectErr:      true,
			expectCalls:    0,
		},
	}

	for testName, tc := range testCases {
		t.Run(testName, func(t *testing.T) {
			callsCount = 0
			handler := func(w http.ResponseWriter, r *http.Request) {
				atomic.AddInt64(&callsCount, 1)
				w.WriteHeader(tc.responseStatus)
				w.Write([]byte(tc.responseBody))
			}
			testServer := httptest.NewServer(http.HandlerFunc(handler))
			defer testServer.Close()

			fetcher, err := trs.NewRulesObjstoreFetcher(testServer.URL, tc.tenants, testServer.Client())
			assert.NoError(t, err)

			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Hour)
			if tc.ctxCancelled {
				cancel()
			} else {
				defer cancel()
			}

			dataReader, err := fetcher.GetTenantsRules(ctx)
			if tc.expectErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)

			assert.EqualValues(t, tc.expectCalls, callsCount)

			data, err := io.ReadAll(dataReader)
			assert.NoError(t, err)

			var ruleGroups *rulefmt.RuleGroups
			if len(data) > 0 {
				var errors []error
				ruleGroups, errors = rulefmt.Parse(data)
				assert.Len(t, errors, 0)

				// Check that rule groups are prefixed with tenant name
				tenantsMap := make(map[string]bool)
				for _, tenant := range tc.tenants {
					tenantsMap[tenant] = true
				}
				for _, group := range ruleGroups.Groups {
					_, ok := tenantsMap[strings.Split(group.Name, ".")[0]]
					assert.True(t, ok)
				}
			}
		})
	}
}
