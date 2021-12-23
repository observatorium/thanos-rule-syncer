package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"sync"
	"time"

	"github.com/coreos/go-oidc"
	"github.com/ghodss/yaml"
	"github.com/oklog/run"
	"github.com/prometheus/client_golang/prometheus"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
)

const (
	fetchConcurrency int = 5
)

type config struct {
	observatoriumURL  string
	observatoriumCA   string
	thanosRuleURL     string
	file              string
	tenantsConfigFile string
	interval          uint
}

type tenantsConfig struct {
	Tenants []tenant `json:"tenants"`
}

type tenant struct {
	Name   string     `json:"name"`
	OIDC   oidcConfig `json:"oidc"`
	client *http.Client
}

type oidcConfig struct {
	Audience     string `json:"audience"`
	ClientID     string `json:"clientID"`
	ClientSecret string `json:"clientSecret"`
	IssuerURL    string `json:"issuerURL"`
}

func parseFlags() *config {
	cfg := &config{}
	flag.StringVar(&cfg.observatoriumURL, "observatorium-api-url", "", "The URL of the Observatorium API from which to fetch the rules.")
	flag.StringVar(&cfg.tenantsConfigFile, "tenants.config-file", "", "The path to the file containing all tenants to sync rules for.")
	flag.StringVar(&cfg.observatoriumCA, "observatorium-ca", "", "Path to a file containing the TLS CA against which to verify the Observatorium API. If no server CA is specified, the client will use the system certificates.")
	flag.StringVar(&cfg.thanosRuleURL, "thanos-rule-url", "", "The URL of Thanos Ruler that is used to trigger reloads of rules. We will append /-/reload.")
	flag.StringVar(&cfg.file, "file", "", "The path to the file the rules are written to on disk so that Thanos Ruler can read it from.")
	flag.UintVar(&cfg.interval, "interval", 60, "The interval at which to poll the Observatorium API for updates to rules, given in seconds.")

	flag.Parse()
	return cfg
}

func main() {
	cfg := parseFlags()

	registry := prometheus.NewRegistry()

	ctx, cancel := context.WithCancel(context.Background())
	client := &http.Client{}
	t := http.DefaultTransport.(*http.Transport).Clone()

	if cfg.observatoriumCA != "" {
		caFile, err := ioutil.ReadFile(cfg.observatoriumCA)
		if err != nil {
			log.Fatalf("failed to read Observatorium CA file: %v", err)
		}

		certPool := x509.NewCertPool()
		certPool.AppendCertsFromPEM(caFile)
		t.TLSClientConfig = &tls.Config{
			RootCAs: certPool,
		}
	}

	var tenantsCfg tenantsConfig
	{
		tenantsRaw, err := ioutil.ReadFile(cfg.tenantsConfigFile)
		if err != nil {
			log.Fatalf("failed to read tenants config file: %v", err)
		}

		if err := yaml.Unmarshal(tenantsRaw, &tenantsCfg); err != nil {
			log.Fatalf("failed to parse tenants config file: %v", err)
		}
	}

	ins := newRoundTripperInstrumenter(registry)

	for i, tenant := range tenantsCfg.Tenants {
		if tenant.OIDC.IssuerURL != "" {
			provider, err := oidc.NewProvider(context.Background(), tenant.OIDC.IssuerURL)
			if err != nil {
				log.Fatalf("OIDC provider initialization failed: %v", err)
			}
			ctxo := context.WithValue(ctx, oauth2.HTTPClient, http.Client{
				Transport: ins.NewRoundTripper("oauth", tenant.Name, http.DefaultTransport),
			})
			ccc := clientcredentials.Config{
				ClientID:     tenant.OIDC.ClientID,
				ClientSecret: tenant.OIDC.ClientSecret,
				TokenURL:     provider.Endpoint().TokenURL,
			}
			if tenant.OIDC.Audience != "" {
				ccc.EndpointParams = url.Values{
					"audience": []string{tenant.OIDC.Audience},
				}
			}
			tenantsCfg.Tenants[i].client = &http.Client{
				Transport: &oauth2.Transport{
					Base:   t,
					Source: ccc.TokenSource(ctxo),
				},
			}
		}
	}

	u, err := url.Parse(cfg.observatoriumURL)
	if err != nil {
		log.Fatalf("failed to parse Observatorium API URL: %v", err)
	}

	var gr run.Group
	gr.Add(run.SignalHandler(ctx, os.Interrupt))

	gr.Add(func() error {
		fn := func(ctx context.Context) error {
			rules, err := getRulesForTenants(ctx, *u, tenantsCfg.Tenants)
			if err != nil {
				return fmt.Errorf("failed to get rules from url: %v\n", err)
			}
			file, err := os.Create(cfg.file)
			if err != nil {
				return fmt.Errorf("failed to create or open the rules file %s: %v", cfg.file, err)
			}
			w := bufio.NewWriter(file)
			if _, err = w.ReadFrom(rules); err != nil {
				return fmt.Errorf("failed to write to rules file %s: %v", cfg.file, err)
			}
			if err := file.Close(); err != nil {
				return fmt.Errorf("failed to close the rules file %s: %v", cfg.file, err)
			}
			if err := reloadThanosRule(ctx, client, cfg.thanosRuleURL); err != nil {
				return fmt.Errorf("failed to trigger thanos rule reload: %v", err)
			}
			return nil
		}
		if err := fn(ctx); err != nil {
			log.Print(err.Error())
		}
		ticker := time.NewTicker(time.Duration(cfg.interval) * time.Second)
		for {
			select {
			case <-ticker.C:
				if err := fn(ctx); err != nil {
					log.Print(err.Error())
				}
			case <-ctx.Done():
				return nil
			}
		}
	}, func(err error) {
		cancel()
	})

	if err := gr.Run(); err != nil {
		log.Fatalf("thanos-rule-syncer quit unexpectectly: %v", err)
	}
}

func getRulesForTenants(ctx context.Context, observatoriumURL url.URL, tenants []tenant) (io.Reader, error) {
	queue := make(chan tenant)
	respc := make(chan []byte)

	go func() {
		var wg sync.WaitGroup
		wg.Add(len(tenants))

		for i := 0; i < fetchConcurrency; i++ {
			go func() {
				for tenant := range queue {
					observatoriumURL.Path = path.Join("/api/metrics/v1", tenant.Name, "/api/v1/rules/raw")
					// TODO(onprem): Handle errors.
					rules, _ := getRules(ctx, tenant.client, observatoriumURL.String())
					defer rules.Close()

					raw, _ := ioutil.ReadAll(rules)

					respc <- raw
					wg.Done()
				}
			}()
		}
		wg.Wait()
		close(respc)
	}()

	go func() {
		for _, tenant := range tenants {
			queue <- tenant
		}
		close(queue)
	}()

	var rules []byte

	for res := range respc {
		merged, err := mergePromRules(rules, res)
		if err != nil {
			continue
		}
		rules = merged
	}

	return bytes.NewReader(rules), nil
}

func getRules(ctx context.Context, client *http.Client, url string) (io.ReadCloser, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req = req.WithContext(ctx)

	res, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to do http request: %w", err)
	}
	if res.StatusCode/100 != 2 {
		return nil, fmt.Errorf("got unexpected status from Observatorium API: %d", res.StatusCode)
	}

	return res.Body, nil
}

func reloadThanosRule(ctx context.Context, client *http.Client, url string) error {
	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/-/reload", url), nil)
	if err != nil {
		return err
	}
	req = req.WithContext(ctx)

	res, err := client.Do(req)
	if err != nil {
		return err
	}
	if res.StatusCode/100 != 2 {
		return fmt.Errorf("got unexpected status from Thanos Ruler: %d", res.StatusCode)
	}

	return nil
}

// merePromRules merges two Prometheus rules files into one.
func mergePromRules(aRaw, bRaw []byte) ([]byte, error) {
	var a, b struct {
		Groups []interface{} `json:"groups"`
	}

	if err := yaml.Unmarshal(aRaw, &a); err != nil {
		return nil, err
	}

	if err := yaml.Unmarshal(bRaw, &b); err != nil {
		return nil, err
	}

	a.Groups = append(a.Groups, b.Groups...)

	return yaml.Marshal(a)
}
