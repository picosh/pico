FROM --platform=$BUILDPLATFORM golang:1.23 AS builder-deps
LABEL maintainer="Pico Maintainers <hello@pico.sh>"

WORKDIR /app

RUN apt-get update
RUN apt-get install -y git ca-certificates

RUN git clone https://codeberg.org/emersion/soju.git

COPY subscriber-change.diff .

WORKDIR /app/soju

RUN git apply ../subscriber-change.diff

RUN --mount=type=cache,target=/go/pkg/,rw \
  --mount=type=cache,target=/root/.cache/,rw \
  go mod download

FROM builder-deps AS builder

COPY . .

ARG APP=prose
ARG TARGETOS
ARG TARGETARCH

ENV CGO_ENABLED=0
ENV GOFLAGS="-tags=moderncsqlite"
ENV LDFLAGS="-s -w"

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
