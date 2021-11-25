package main

import (
	"bufio"
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
	"time"

	"github.com/coreos/go-oidc"
	"github.com/oklog/run"
	"github.com/prometheus/client_golang/prometheus"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
)

type config struct {
	observatoriumURL string
	observatoriumCA  string
	thanosRuleURL    string
	file             string
	tenant           string
	oidc             oidcConfig
	interval         uint
}

type oidcConfig struct {
	audience     string
	clientID     string
	clientSecret string
	issuerURL    string
}

func parseFlags() *config {
	cfg := &config{}
	flag.StringVar(&cfg.observatoriumURL, "observatorium-api-url", "", "The URL of the Observatorium API from which to fetch the rules.")
	flag.StringVar(&cfg.tenant, "tenant", "", "The name of the tenant whose rules should be synced.")
	flag.StringVar(&cfg.observatoriumCA, "observatorium-ca", "", "Path to a file containing the TLS CA against which to verify the Observatorium API. If no server CA is specified, the client will use the system certificates.")
	flag.StringVar(&cfg.thanosRuleURL, "thanos-rule-url", "", "The URL of Thanos Ruler that is used to trigger reloads of rules. We will append /-/reload.")
	flag.StringVar(&cfg.file, "file", "", "The path to the file the rules are written to on disk so that Thanos Ruler can read it from.")
	flag.StringVar(&cfg.oidc.issuerURL, "oidc.issuer-url", "", "The OIDC issuer URL, see https://openid.net/specs/openid-connect-discovery-1_0.html#IssuerDiscovery.")
	flag.StringVar(&cfg.oidc.clientSecret, "oidc.client-secret", "", "The OIDC client secret, see https://tools.ietf.org/html/rfc6749#section-2.3.")
	flag.StringVar(&cfg.oidc.clientID, "oidc.client-id", "", "The OIDC client ID, see https://tools.ietf.org/html/rfc6749#section-2.3.")
	flag.StringVar(&cfg.oidc.audience, "oidc.audience", "", "The audience for whom the access token is intended, see https://openid.net/specs/openid-connect-core-1_0.html#IDToken.")
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

	if cfg.oidc.issuerURL != "" {
		provider, err := oidc.NewProvider(context.Background(), cfg.oidc.issuerURL)
		if err != nil {
			log.Fatalf("OIDC provider initialization failed: %v", err)
		}
		ctx = context.WithValue(ctx, oauth2.HTTPClient, http.Client{
			Transport: newRoundTripperInstrumenter(registry).NewRoundTripper("oauth", http.DefaultTransport),
		})
		ccc := clientcredentials.Config{
			ClientID:     cfg.oidc.clientID,
			ClientSecret: cfg.oidc.clientSecret,
			TokenURL:     provider.Endpoint().TokenURL,
		}
		if cfg.oidc.audience != "" {
			ccc.EndpointParams = url.Values{
				"audience": []string{cfg.oidc.audience},
			}
		}
		client = &http.Client{
			Transport: &oauth2.Transport{
				Base:   t,
				Source: ccc.TokenSource(ctx),
			},
		}

	}

	u, err := url.Parse(cfg.observatoriumURL)
	if err != nil {
		log.Fatalf("failed to parse Observatorium API URL: %v", err)
	}
	u.Path = path.Join("/api/metrics/v1", cfg.tenant, "/api/v1/rules/raw")

	var gr run.Group
	gr.Add(run.SignalHandler(ctx, os.Interrupt))

	gr.Add(func() error {
		fn := func(ctx context.Context) error {
			rules, err := getRules(ctx, client, u.String())
			if err != nil {
				return fmt.Errorf("failed to get rules from url: %v\n", err)
			}
			defer rules.Close()
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
