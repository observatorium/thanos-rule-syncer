package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"gopkg.in/yaml.v3"
)

func TestTenantsConfig(t *testing.T) {
	testCases := map[string]struct {
		fileContent   TenantsConfig
		expectErr     bool
		expectTenants []string
		expectPanics  bool
	}{
		"empty file": {
			fileContent: TenantsConfig{},
			expectErr:   true,
		},
		"valid tenants": {
			fileContent: TenantsConfig{
				Tenants: []TenantConfig{
					{
						ID: "tenant1",
					},
					{
						ID: "tenant2",
					},
				},
			},
			expectTenants: []string{"tenant1", "tenant2"},
		},
		"multiple tenants with duplicates": {
			fileContent: TenantsConfig{
				Tenants: []TenantConfig{
					{
						ID: "tenant1",
					},
					{
						ID: "tenant1",
					},
				},
			},
			expectErr: true,
		},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			tenantsCfg, err := yaml.Marshal(tc.fileContent)
			assert.NoError(t, err)

			tenants, err := readTenantsConfig(tenantsCfg)
			if tc.expectErr {
				assert.Error(t, err)
				return
			}

			assert.NoError(t, err)
			assert.Len(t, tenants, len(tc.expectTenants))
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
