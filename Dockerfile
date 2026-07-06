# Multi-stage build. GoReleaser injects the pre-built binary via COPY.
# For local builds, use `docker build -f Dockerfile.dev .` (source-based).
FROM --platform=$BUILDPLATFORM alpine:3.20 AS certs
RUN apk --no-cache add ca-certificates

FROM scratch
COPY --from=certs /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY iceberg-sentry /usr/local/bin/iceberg-sentry

# Non-root user
USER 65534:65534

ENV HOME=/tmp
WORKDIR /tmp

ENTRYPOINT ["/usr/local/bin/iceberg-sentry"]
CMD ["--help"]
