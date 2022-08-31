FROM --platform=$BUILDPLATFORM golang:1.19 as builder-deps
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

ARG APP=lists
ARG TARGETOS
ARG TARGETARCH

ENV CGO_ENABLED=1
ENV LDFLAGS="-s -w -linkmode external -extldflags '-static -lm -pthread'"
ENV CC=/app/scripts/gccwrap.sh

ENV GOOS=${TARGETOS} GOARCH=${TARGETARCH}

RUN go build -ldflags "$LDFLAGS" -tags "netgo osusergo" -o /go/bin/${APP}-ssh ./cmd/${APP}/ssh
RUN go build -ldflags "$LDFLAGS" -tags "netgo osusergo" -o /go/bin/${APP}-web ./cmd/${APP}/web

FROM scratch as release-ssh

WORKDIR /app

ARG APP=lists

COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /go/bin/${APP}-ssh ./ssh

ENTRYPOINT ["/app/ssh"]

FROM scratch as release-web

WORKDIR /app

ARG APP=lists

COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /go/bin/${APP}-web ./web
COPY --from=builder /app/${APP}/html ./${APP}/html
COPY --from=builder /app/${APP}/public ./${APP}/public

ENTRYPOINT ["/app/web"]
