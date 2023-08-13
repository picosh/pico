FROM caddy:builder-alpine AS builder

# https://github.com/caddyserver/caddy-docker/issues/307#issuecomment-1670301042
RUN xcaddy build v2.7.3 \
    --with github.com/caddy-dns/cloudflare

FROM caddy:alpine

COPY --from=builder /usr/bin/caddy /usr/bin/caddy
