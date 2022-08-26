FROM --platform=$BUILDPLATFORM golang:1.19-alpine as builder-deps
LABEL maintainer="Pico Maintainers <hello@pico.sh>"

ENV CGO_ENABLED 0

WORKDIR /app

RUN apk add --no-cache git ca-certificates

COPY go.* ./

RUN go mod download

FROM builder-deps as builder

COPY . .

ARG APP=lists
ARG TARGETOS
ARG TARGETARCH

ENV GOOS=${TARGETOS} GOARCH=${TARGETARCH}

RUN go build -o /go/bin/${APP}-ssh -ldflags="-s -w" ./cmd/${APP}/ssh
RUN go build -o /go/bin/${APP}-web -ldflags="-s -w" ./cmd/${APP}/web

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
