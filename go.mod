module github.com/observatorium/thanos-rule-syncer

go 1.15

require (
	github.com/campoy/embedmd v1.0.0
	github.com/coreos/go-oidc v2.2.1+incompatible
	github.com/observatorium/api v0.1.2
	github.com/oklog/run v1.1.0
	github.com/pquerna/cachecontrol v0.0.0-20201205024021-ac21108117ac // indirect
	github.com/prometheus/client_golang v1.8.0
	golang.org/x/oauth2 v0.0.0-20201208152858-08078c50e5b5
	gopkg.in/square/go-jose.v2 v2.5.1 // indirect
)

replace github.com/observatorium/api v0.1.2 => github.com/allenmqcymp/observatorium v0.1.2-0.20210621173933-87f97838a886
