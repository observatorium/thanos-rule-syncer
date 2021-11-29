FROM golang:1.16-alpine3.14 as builder

RUN apk add --update --no-cache ca-certificates tzdata git make bash && update-ca-certificates

ADD . /opt
WORKDIR /opt

RUN git update-index --refresh; make build

FROM alpine:3.14 as runner

COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /opt/thanos-rule-syncer /bin/thanos-rule-syncer

ARG BUILD_DATE
ARG VERSION
ARG VCS_REF
ARG DOCKERFILE_PATH

LABEL vendor="Observatorium" \
    name="observatorium/thanos-rule-syncer" \
    description="Thanos Rule Syncer" \
    io.k8s.display-name="observatorium/thanos-rule-syncer" \
    io.k8s.description="Thanos Rule Syncer" \
    maintainer="Observatorium <team-monitoring@redhat.com>" \
    version="$VERSION" \
    org.label-schema.build-date=$BUILD_DATE \
    org.label-schema.description="Thanos Rule Syncer" \
    org.label-schema.docker.cmd="docker run --rm observatorium/thanos-rule-syncer" \
    org.label-schema.docker.dockerfile=$DOCKERFILE_PATH \
    org.label-schema.name="observatorium/thanos-rule-syncer" \
    org.label-schema.schema-version="1.0" \
    org.label-schema.vcs-branch=$VCS_BRANCH \
    org.label-schema.vcs-ref=$VCS_REF \
    org.label-schema.vcs-url="https://github.com/observatorium/thanos-rule-syncer" \
    org.label-schema.vendor="observatorium/thanos-rule-syncer" \
    org.label-schema.version=$VERSION

ENTRYPOINT ["/bin/thanos-rule-syncer"]
