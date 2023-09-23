FROM --platform=$BUILDPLATFORM golang:1.21 as builder-deps
LABEL maintainer="Pico Maintainers <hello@pico.sh>"

WORKDIR /app

RUN dpkg --add-architecture arm64 && dpkg --add-architecture amd64
RUN apt-get update
RUN apt-get install -y git ca-certificates \
    libwebp-dev:amd64 libwebp-dev:arm64 \
    crossbuild-essential-amd64 crossbuild-essential-arm64 \
    libc-dev:amd64 libc-dev:arm64

COPY go.* ./

RUN go mod download

FROM builder-deps as builder

COPY . .

ARG TARGETOS
ARG TARGETARCH

ENV CGO_ENABLED=1
ENV LDFLAGS="-s -w -linkmode external -extldflags '-static -lm -pthread'"
ENV CC=/app/scripts/gccwrap.sh

ENV GOOS=${TARGETOS} GOARCH=${TARGETARCH}

RUN go build -ldflags "$LDFLAGS" -tags "netgo osusergo" -o /go/bin/auth ./cmd/auth

FROM scratch as release-web

WORKDIR /app

ARG APP=lists

COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /go/bin/auth ./web

ENTRYPOINT ["/app/web"]
