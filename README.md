# thanos-rule-syncer

[![Go Report Card](https://goreportcard.com/badge/github.com/observatorium/thanos-rule-syncer)](https://goreportcard.com/report/github.com/observatorium/thanos-rule-syncer)

`thanos-rule-syncer` is a small process that can be run as sidecar to synchronize Prometheus rules from multi tenant APIs to the Thanos Ruler.
It will first fetch the tenant's rules from the given `--observatorium-api-url` which should be the full URL including the path.
Next the rules will be written to disk which should be the same folder that your Thanos Rule can read rules from.
At last the Thanos Ruler will be reloaded with a POST request against `$(--thanos-rule-url)/-/reload`.

## Usage

[embedmd]:# (tmp/help.txt)
```txt
Usage of ./thanos-rule-syncer:
  -file string
    	The path to the file the rules are written to on disk so that Thanos Ruler can read it from.
  -interval uint
    	The interval at which to poll the Observatorium API for updates to rules, given in seconds. (default 60)
  -observatorium-api-url string
    	The URL of the Observatorium API from which to fetch the rules.
  -observatorium-ca string
    	Path to a file containing the TLS CA against which to verify the Observatorium API. If no server CA is specified, the client will use the system certificates.
  -oidc.audience string
    	The audience for whom the access token is intended, see https://openid.net/specs/openid-connect-core-1_0.html#IDToken.
  -oidc.client-id string
    	The OIDC client ID, see https://tools.ietf.org/html/rfc6749#section-2.3.
  -oidc.client-secret string
    	The OIDC client secret, see https://tools.ietf.org/html/rfc6749#section-2.3.
  -oidc.issuer-url string
    	The OIDC issuer URL, see https://openid.net/specs/openid-connect-discovery-1_0.html#IssuerDiscovery.
  -tenant string
    	The name of the tenant whose rules should be synced.
  -thanos-rule-url string
    	The URL of Thanos Ruler that is used to trigger reloads of rules. We will append /-/reload.
```
