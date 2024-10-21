FROM --platform=$BUILDPLATFORM caddy:builder-alpine AS builder

ENV CGO_ENABLED=0

ARG TARGETOS
ARG TARGETARCH

ENV GOOS=${TARGETOS} GOARCH=${TARGETARCH}

RUN xcaddy build \
    --with github.com/caddy-dns/cloudflare \
    --with github.com/darkweak/souin/plugins/caddy@v1.7.2 \
    --with github.com/darkweak/storages/otter/caddy

FROM caddy:alpine

COPY --from=builder /usr/bin/caddy /usr/bin/caddy
