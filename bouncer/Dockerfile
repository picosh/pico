FROM golang:1.19-alpine as builder

WORKDIR /app

RUN apk add --no-cache git sqlite-libs sqlite-dev build-base

RUN git clone https://github.com/emersion/soju

WORKDIR /app/soju

ENV LDFLAGS="-linkmode external -extldflags -static"
ENV GOFLAGS="-tags=moderncsqlite"

RUN go build -ldflags "$LDFLAGS" ./cmd/soju
RUN go build -ldflags "$LDFLAGS" ./cmd/sojuctl

FROM alpine

WORKDIR /app

ADD ./bouncer/soju.config .
COPY --from=builder /app/soju/soju .
COPY --from=builder /app/soju/sojuctl .

EXPOSE 6697
EXPOSE 8080

ENTRYPOINT ["/app/soju", "-config", "/app/soju.config"]
