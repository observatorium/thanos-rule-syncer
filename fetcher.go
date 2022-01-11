package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"

	rulesspec "github.com/observatorium/api/rules"
)

type fetcher interface {
	getRules(ctx context.Context) (rules io.ReadCloser, err error)
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

// rulesBackendFetcher fetches rules for all tenants from Rules Storage Backend.
type rulesBackendFetcher struct {
	client rulesspec.ClientInterface
}

func newRulesBackendFetcher(baseURL string, client *http.Client) (*rulesBackendFetcher, error) {
	rulesClient, err := rulesspec.NewClient(baseURL, rulesspec.WithHTTPClient(client))
	if err != nil {
		return nil, fmt.Errorf("failed to create rules backend client: %w", err)
	}

	return &rulesBackendFetcher{
		client: rulesClient,
	}, nil
}

func (f *rulesBackendFetcher) getRules(ctx context.Context) (io.ReadCloser, error) {
	res, err := f.client.ListAllRules(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to do http request: %w", err)
	}
	if res.StatusCode/100 != 2 {
		return nil, fmt.Errorf("got unexpected status from rules backend: %d", res.StatusCode)
	}

	return res.Body, nil
}
