package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"

	rulesspec "github.com/observatorium/api/rules"
	"github.com/prometheus/prometheus/model/rulefmt"
	"gopkg.in/yaml.v3"
)

// RulesObjtoreFetcher fetches rules for all configured tenants from the rules-objstore.
type RulesObjtoreFetcher struct {
	client     rulesspec.ClientInterface
	tenants    []string
	tenantsMtx sync.Mutex
}

// NewRulesObjtoreFetcher creates a new RulesObjtoreFetcher.
// The tenants list must be deduplicated otherwise, rules groups will not be unique.
func NewRulesObjtoreFetcher(baseURL string, tenants []string, client *http.Client) (*RulesObjtoreFetcher, error) {
	if client == nil {
		client = http.DefaultClient
	}

	baseURLParsed, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse RulesObjtoreFetcher URL: %w", err)
	}

	rulesClient, err := rulesspec.NewClient(baseURLParsed.String(), rulesspec.WithHTTPClient(client))
	if err != nil {
		return nil, fmt.Errorf("failed to create rules-objstore client: %w", err)
	}

	return &RulesObjtoreFetcher{
		client:  rulesClient,
		tenants: tenants,
	}, nil
}

type tenantFetchResult struct {
	tenant string
	res    *http.Response
	err    error
}

// GetTenantsRules fetches rules for all configured tenants from the rules-objstore.
func (f *RulesObjtoreFetcher) GetTenantsRules(ctx context.Context) (io.ReadCloser, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	concurrency := 10
	var sem = make(chan struct{}, concurrency)
	results := make(chan tenantFetchResult)

	// Launch goroutines that fetch rules for each tenant concurrently.
	go func() {
		var wg sync.WaitGroup
		defer close(results)
		defer wg.Wait() // Wait for all goroutines to finish before closing results channel.

		// tenants can be changed concurrently, we copy the list to avoid locking for too long.
		f.tenantsMtx.Lock()
		tenants := make([]string, len(f.tenants))
		copy(tenants, f.tenants)
		f.tenantsMtx.Unlock()

		for _, tenantID := range f.tenants {
			// Use semaphore to limit concurrency, and return early if context is cancelled.
			select {
			case <-ctx.Done():
				results <- tenantFetchResult{tenantID, nil, ctx.Err()}
				return
			case sem <- struct{}{}:
			}

			// Launch goroutine to fetch rules for a tenant.
			wg.Add(1)
			go func(tenantID string) {
				defer wg.Done()
				defer func() { <-sem }()
				res, err := f.client.ListRules(ctx, tenantID)
				results <- tenantFetchResult{tenantID, res, err}
			}(tenantID)
		}
	}()

	// Consume results and return on first error.
	// Returning cancels the context, which in turn cancels all goroutines.
	var rules []rulefmt.RuleGroup
	for result := range results {
		if result.err != nil {
			return nil, fmt.Errorf("failed to do http request: %w", result.err)
		}

		if result.res.StatusCode/100 != 2 {
			return nil, fmt.Errorf("got unexpected status from Observatorium API: %d", result.res.StatusCode)
		}

		// Read and parse response body
		body, err := io.ReadAll(result.res.Body)
		if err != nil {
			return nil, fmt.Errorf("failed to read response body: %w", err)
		}
		result.res.Body.Close()

		rulesParsed, errors := rulefmt.Parse(body)
		if len(errors) > 0 {
			return nil, fmt.Errorf(aggregateErrorMessages(errors))
		}

		// Prepend tenant name to all rules group names to avoid conflicts
		// This reflects the behavior of the rules-objstore api for ListAllRules.
		for i, group := range rulesParsed.Groups {
			rulesParsed.Groups[i].Name = result.tenant + "." + group.Name
		}

		rules = append(rules, rulesParsed.Groups...)
	}

	returnData, err := yaml.Marshal(rulefmt.RuleGroups{Groups: rules})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal rules: %w", err)
	}

	ret := io.NopCloser(bytes.NewReader(returnData))
	return ret, nil
}

// GetAllRules fetches all rules from the rules-objstore.
func (f *RulesObjtoreFetcher) GetAllRules(ctx context.Context) (io.ReadCloser, error) {
	res, err := f.client.ListAllRules(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to do http request: %w", err)
	}
	if res.StatusCode/100 != 2 {
		return nil, fmt.Errorf("got unexpected status from rules backend: %d", res.StatusCode)
	}

	return res.Body, nil
}

// SetTenants sets the tenants to fetch rules for.
// This method is thread-safe.
func (f *RulesObjtoreFetcher) SetTenants(tenants []string) {
	f.tenantsMtx.Lock()
	f.tenants = tenants
	f.tenantsMtx.Unlock()
}

// observatoriumAPIFetcher fetches rules for a tenant from Observatorium API.
type observatoriumAPIFetcher struct {
	endpoint *url.URL
	client   *http.Client
}

func newObservatoriumAPIFetcher(baseURL string, tenant string, client *http.Client) (*observatoriumAPIFetcher, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse Observatorium API URL: %w", err)
	}

	u.Path = path.Join("/api/metrics/v1", tenant, "/api/v1/rules/raw")

	return &observatoriumAPIFetcher{
		endpoint: u,
		client:   client,
	}, nil
}

func (f *observatoriumAPIFetcher) getRules(ctx context.Context) (io.ReadCloser, error) {
	req, err := http.NewRequest(http.MethodGet, f.endpoint.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req = req.WithContext(ctx)

	res, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to do http request: %w", err)
	}
	if res.StatusCode/100 != 2 {
		return nil, fmt.Errorf("got unexpected status from Observatorium API: %d", res.StatusCode)
	}

	return res.Body, nil
}

func aggregateErrorMessages(errs []error) string {
	var builder strings.Builder

	for i, err := range errs {
		if i > 0 {
			builder.WriteString(", ")
		}
		builder.WriteString(err.Error())
	}

	return builder.String()
}
