FROM --platform=$BUILDPLATFORM golang:1.27 AS builder-deps
LABEL maintainer="Pico Maintainers <hello@pico.sh>"

WORKDIR /app

RUN apt-get update
RUN apt-get install -y git ca-certificates

RUN git clone https://codeberg.org/emersion/soju.git

COPY bouncer/subscriber-change.diff bouncer/network-security.diff ./

WORKDIR /app/soju

RUN git checkout v0.10.1

RUN git apply ../subscriber-change.diff
RUN git apply ../network-security.diff

COPY pkg/netguard ./netguard

RUN --mount=type=cache,target=/go/pkg/,rw \
  --mount=type=cache,target=/root/.cache/,rw \
  go mod download

FROM builder-deps AS builder

COPY bouncer/ .
COPY bouncer/testdata/network_security_test.go ./network_security_test.go

ARG APP=prose
ARG TARGETOS
ARG TARGETARCH

ENV CGO_ENABLED=0
ENV GOFLAGS="-tags=moderncsqlite"
ENV LDFLAGS="-s -w"

RUN --mount=type=cache,target=/go/pkg/ \
  --mount=type=cache,target=/root/.cache/ \
  go test ./netguard . -run '^Test(Pico|Validate|Dialer|ResponseBody)'

ENV GOOS=${TARGETOS} GOARCH=${TARGETARCH}

RUN --mount=type=cache,target=/go/pkg/,rw \
  --mount=type=cache,target=/root/.cache/,rw \
  go build -ldflags "$LDFLAGS" ./cmd/soju

RUN --mount=type=cache,target=/go/pkg/,rw \
  --mount=type=cache,target=/root/.cache/,rw \
  go build -ldflags "$LDFLAGS" ./cmd/sojuctl

FROM scratch

WORKDIR /app

COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /app/soju/soju .
COPY --from=builder /app/soju/sojuctl .

COPY soju.config .
COPY motd.txt .

EXPOSE 6697
EXPOSE 8080

ENTRYPOINT ["/app/soju", "-config", "/app/soju.config"]
