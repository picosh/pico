FROM --platform=$BUILDPLATFORM golang:1.22 as builder-deps
LABEL maintainer="Pico Maintainers <hello@pico.sh>"

WORKDIR /app

RUN apt-get update
RUN apt-get install -y git ca-certificates

COPY go.* ./

RUN go mod download

FROM builder-deps as builder-web

COPY . .

ARG APP=prose
ARG TARGETOS
ARG TARGETARCH

ENV CGO_ENABLED=0
ENV LDFLAGS="-s -w"

ENV GOOS=${TARGETOS} GOARCH=${TARGETARCH}

RUN go build -ldflags "$LDFLAGS" -o /go/bin/${APP}-web ./cmd/${APP}/web

FROM builder-deps as builder-ssh

COPY . .

ARG APP=prose
ARG TARGETOS
ARG TARGETARCH

ENV CGO_ENABLED=0
ENV LDFLAGS="-s -w"

ENV GOOS=${TARGETOS} GOARCH=${TARGETARCH}

RUN go build -ldflags "$LDFLAGS" -o /go/bin/${APP}-ssh ./cmd/${APP}/ssh

FROM scratch as release-web

WORKDIR /app

ARG APP=prose

COPY --from=builder-web /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder-web /go/bin/${APP}-web ./web
COPY --from=builder-web /app/${APP}/html ./${APP}/html
COPY --from=builder-web /app/${APP}/public ./${APP}/public

ENTRYPOINT ["/app/web"]

FROM scratch as release-ssh

WORKDIR /app
ENV TERM="xterm-256color"

ARG APP=prose

COPY --from=builder-ssh /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder-ssh /go/bin/${APP}-ssh ./ssh


ENTRYPOINT ["/app/ssh"]
