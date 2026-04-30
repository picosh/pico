FROM --platform=$BUILDPLATFORM docker.io/caddy:builder-alpine AS builder

ENV CGO_ENABLED=0

ARG TARGETOS
ARG TARGETARCH

ENV GOOS=${TARGETOS} GOARCH=${TARGETARCH}

RUN xcaddy build \
    --with github.com/caddy-dns/cloudflare

FROM docker.io/caddy:alpine

COPY --from=builder /usr/bin/caddy /usr/bin/caddy
