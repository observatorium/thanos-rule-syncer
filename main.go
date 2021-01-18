package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/coreos/go-oidc"
	"github.com/oklog/run"
	"github.com/prometheus/client_golang/prometheus"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
)

type config struct {
	observatoriumURL string
	thanosRuleURL    string
	file             string
	oidc             oidcConfig
}

type oidcConfig struct {
	audience     string
	clientID     string
	clientSecret string
	issuerURL    string
}

func parseFlags() *config {
	cfg := &config{}
	flag.StringVar(&cfg.observatoriumURL, "observatorium-api-url", "", "The URL of the Observatorium API from where to fetch the rules. This should be the full ULR including the path to the tenant's rules.")
	flag.StringVar(&cfg.thanosRuleURL, "thanos-rule-url", "", "The URL of Thanos Ruler that is used to trigger reloads of rules. We will append /-/reload.")
	flag.StringVar(&cfg.file, "file", "", "The path to the file the rules are written to on disk so that Thanos Ruler can read it from.")
	flag.StringVar(&cfg.oidc.issuerURL, "oidc.issuer-url", "", "The OIDC issuer URL, see https://openid.net/specs/openid-connect-discovery-1_0.html#IssuerDiscovery.")
	flag.StringVar(&cfg.oidc.clientSecret, "oidc.client-secret", "", "The OIDC client secret, see https://tools.ietf.org/html/rfc6749#section-2.3.")
	flag.StringVar(&cfg.oidc.clientID, "oidc.client-id", "", "The OIDC client ID, see https://tools.ietf.org/html/rfc6749#section-2.3.")
	flag.StringVar(&cfg.oidc.audience, "oidc.audience", "", "The audience for whom the access token is intended, see https://openid.net/specs/openid-connect-core-1_0.html#IDToken.")

	flag.Parse()
	return cfg
}

func main() {
	cfg := parseFlags()

	registry := prometheus.NewRegistry()

	ctx, cancel := context.WithCancel(context.Background())
	client := &http.Client{}

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

		caFile, err := ioutil.ReadFile("../observatorium/tmp/certs/ca.pem")
		if err != nil {
			log.Fatalf("failed to read CA file: %v", err)
		}

		certPool := x509.NewCertPool()
		certPool.AppendCertsFromPEM(caFile)

		clientCert, err := tls.LoadX509KeyPair("../observatorium/tmp/certs/client.pem", "../observatorium/tmp/certs/client.key")
		if err != nil {
			log.Fatalf("failed to load client key pair: %v", err)
		}

		tlsConfig := tls.Config{
			RootCAs:      certPool,
			Certificates: []tls.Certificate{clientCert},
		}

		client.Transport = &http.Transport{TLSClientConfig: &tlsConfig}
	}

	var gr run.Group
	gr.Add(run.SignalHandler(ctx, os.Interrupt))

	gr.Add(func() error {
		ticker := time.NewTicker(time.Minute)
		for {
			select {
			case <-ticker.C:
				rules, err := getRules(ctx, client, cfg.observatoriumURL)
				if err != nil {
					log.Printf("failed to get rules from url: %v\n", err)
					continue
				}
				file, err := os.Create(cfg.file)
				if err != nil {
					log.Printf("failed to create or open the rules file %s: %v", cfg.file, err)
					continue
				}
				if _, err = file.Write(rules); err != nil {
					log.Printf("failed to write to rules file %s: %v", cfg.file, err)
					continue
				}
				if err := file.Close(); err != nil {
					log.Printf("failed to close the rules file %s: %v", cfg.file, err)
					continue
				}

				if err := reloadThanosRule(ctx, client, cfg.thanosRuleURL); err != nil {
					log.Printf("failed to trigger thanos rule reload: %v", err)
					continue
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

	fmt.Println("success")
}

func getRules(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req = req.WithContext(ctx)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to do http request: %w", err)
	}

	return ioutil.ReadAll(resp.Body)
}

func reloadThanosRule(ctx context.Context, client *http.Client, url string) error {
	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/-/reload", url), nil)
	if err != nil {
		return err
	}
	req = req.WithContext(ctx)

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("thanos rule didn't not return 200 but %d", resp.StatusCode)
	}

	return nil
}
