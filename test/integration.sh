#!/bin/bash

# Runs a semi-realistic integration test with an Observatorium API
# exposing rules using a file as the rule repository, a thanos-rule-syncer
# configuring a Thanos Ruler, and a Thanos Querier evaluating the rules
# all being authenticated via Hydra.

set -euo pipefail

result=1
trap 'kill $(jobs -p); exit $result' EXIT

(DSN=memory hydra serve all --dangerous-force-http --disable-telemetry --config ./test/config/hydra.yaml) &

echo "-------------------------------------------"
echo "- Waiting for Hydra to come up...  --------"
echo "-------------------------------------------"

until curl --output /dev/null --silent --fail --insecure http://127.0.0.1:4444/.well-known/openid-configuration; do
  printf '.'
  sleep 1
done

echo "-------------------------------------------"
echo "- Registering OIDC clients...         -"
echo "-------------------------------------------"

curl \
    --header "Content-Type: application/json" \
    --request POST \
    --data '{"audience": ["observatorium"], "client_id": "read-only", "client_secret": "secret", "grant_types": ["client_credentials"], "token_endpoint_auth_method": "client_secret_post"}' \
    http://127.0.0.1:4445/clients

curl \
    --header "Content-Type: application/json" \
    --request POST \
    --data '{"audience": ["observatorium"], "client_id": "thanos-rule-syncer", "client_secret": "secret", "grant_types": ["client_credentials"], "token_endpoint_auth_method": "client_secret_basic"}' \
    http://127.0.0.1:4445/clients

token=$(curl \
    --request POST \
    --silent \
    --url http://127.0.0.1:4444/oauth2/token \
    --header 'content-type: application/x-www-form-urlencoded' \
    --data grant_type=client_credentials \
    --data client_id=read-only \
    --data client_secret=secret \
    --data audience=observatorium \
    --data scope="openid" | sed 's/^{.*"access_token":[^"]*"\([^"]*\)".*}/\1/')

(
  observatorium \
    --web.listen=0.0.0.0:8443 \
    --web.internal.listen=0.0.0.0:8448 \
    --web.healthchecks.url=http://127.0.0.1:8443 \
    --metrics.read.endpoint=http://127.0.0.1:9091 \
    --metrics.write.endpoint=http://127.0.0.1:19291 \
    --rbac.config=./test/config/rbac.yaml \
    --tenants.config=./test/config/tenants.yaml \
    --log.level=debug \
    --metrics.rules.endpoint=http://127.0.0.1:8080
) &

(
  { echo -ne "HTTP/1.0 200 OK\r\nContent-Length: $(wc -c < test/config/rules.yaml)\r\nContent-Type: yaml\r\n\r\n"; cat  test/config/rules.yaml; } | nc -Nlp 8080
) &

tmp=$(mktemp -d)
touch "$tmp"/rules.yaml

(
  thanos rule \
    --query=127.0.0.1:9091 \
    --rule-file="$tmp"/rules.yaml \
    --grpc-address=127.0.0.1:10901 \
    --http-address=127.0.0.1:10902 \
    --log.level=error \
    --data-dir="$tmp"
) &

(
  thanos query \
    --grpc-address=127.0.0.1:10911 \
    --http-address=127.0.0.1:9091 \
    --store=127.0.0.1:10901 \
    --log.level=error
) &


echo "-------------------------------------------"
echo " Running thanos-rule-syncer "
echo "-------------------------------------------"

(
  ./thanos-rule-syncer \
    --observatorium-api-url=http://localhost:8443 \
    --tenant=test-oidc \
    --thanos-rule-url=http://localhost:10902 \
    --file="$tmp"/rules.yaml \
    --oidc.issuer-url=http://localhost:4444/ \
    --oidc.client-id=thanos-rule-syncer \
    --oidc.client-secret=secret \
    --oidc.audience=observatorium
) &

echo "-------------------------------------------"
echo "- Waiting for dependencies to come up...  -"
echo "-------------------------------------------"
sleep 10

until curl --output /dev/null --silent --fail http://127.0.0.1:8448/ready; do
  printf '.'
  sleep 1
done

echo "-------------------------------------------"
echo "- Rules File tests                        -"
echo "-------------------------------------------"

query="$(curl \
    --fail \
    'http://127.0.0.1:8443/api/metrics/v1/test-oidc/api/v1/query?query=trs' \
    -H "Authorization: bearer $token")"

if [[ "$query" == *'"result":[{"metric":{"__name__":"trs","tenant_id":"1610b0c3-c509-4592-a256-a1871353dbfa"}'* ]]; then
  result=0
  echo "-------------------------------------------"
  echo "- Rules File: OK                          -"
  echo "-------------------------------------------"
else
  result=1
  echo "-------------------------------------------"
  echo "- Rules File: FAILED                      -"
  echo "-------------------------------------------"
  exit 1
fi

echo "-------------------------------------------"
echo "- All tests: OK                           -"
echo "-------------------------------------------"
exit 0
