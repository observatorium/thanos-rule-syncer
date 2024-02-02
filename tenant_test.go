package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestScanFile(t *testing.T) {
	testCases := map[string]struct {
		fileContent string
		expectErr   bool
		expect      []string
	}{
		"empty file": {
			fileContent: "",
			expectErr:   true,
		},
		"single tenant": {
			fileContent: "tenants:\n- name: tenant1",
			expect:      []string{"tenant1"},
		},
		"multiple tenants": {
			fileContent: "tenants:\n- name: tenant1\n- name: tenant2",
			expect:      []string{"tenant1", "tenant2"},
		},
		"multiple tenants with duplicates": {
			fileContent: "tenants:\n- name: tenant1\n- name: tenant1\n- name: tenant2",
			expect:      []string{"tenant1", "tenant2"},
		},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			tenants, err := scanFile([]byte(tc.fileContent))
			if tc.expectErr {
				assert.Error(t, err)
				return
			}

			assert.NoError(t, err)
			assert.Len(t, tenants, len(tc.expect))

			expectedTenants := make(map[string]struct{}, len(tc.expect))
			for _, tenant := range tc.expect {
				expectedTenants[tenant] = struct{}{}
			}

			for _, tenant := range tenants {
				_, ok := expectedTenants[tenant]
				assert.True(t, ok)
				delete(expectedTenants, tenant)
			}
		})
	}
}

type testTenantsSetterFunc func(tenants []string) error

func (f testTenantsSetterFunc) SetTenants(tenants []string) {
	f(tenants)
}

func TestTenantsFileReloader(t *testing.T) {
	testCases := map[string]struct {
		tenantsReader            func() ([]string, error)
		interval                 time.Duration
		contextDuration          time.Duration
		expectTenantsUpdateCalls int
		expectErr                bool
	}{
		"reloads tenants until context cancel": {
			tenantsReader: func() ([]string, error) {
				return []string{"tenant1"}, nil
			},
			interval:                 100 * time.Millisecond,
			contextDuration:          250 * time.Millisecond,
			expectTenantsUpdateCalls: 2,
		},
		"3 errors in a row exits with error": {
			tenantsReader: func() ([]string, error) {
				return nil, errors.New("test error")
			},
			interval:                 100 * time.Millisecond,
			contextDuration:          1000 * time.Millisecond,
			expectTenantsUpdateCalls: 0,
			expectErr:                true,
		},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			tenantsUpdateCalls := 0
			tenantsUpdate := func(tenants []string) error {
				tenantsUpdateCalls++
				return nil
			}

			ctx, cancel := context.WithTimeout(context.Background(), tc.contextDuration)
			defer cancel()

			err := newTenantsFileReloader(ctx, tc.tenantsReader, tc.interval, testTenantsSetterFunc(tenantsUpdate))
			if tc.expectErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

			assert.Equal(t, tc.expectTenantsUpdateCalls, tenantsUpdateCalls)
		})
	}
}
