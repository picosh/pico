FROM golang:1.18.1-alpine3.15 AS builder

RUN apk add --no-cache git

WORKDIR /app
COPY . ./

RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o ./build/ssh ./cmd/lists/ssh
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o ./build/web ./cmd/lists/web
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o ./build/gemini ./cmd/lists/gemini

FROM alpine:3.15 AS ssh
WORKDIR /app
COPY --from=0 /app/build/ssh ./
CMD ["./ssh"]

FROM alpine:3.15 AS web
WORKDIR /app
COPY --from=0 /app/build/web ./
COPY --from=0 /app/html ./html
COPY --from=0 /app/public ./public
CMD ["./web"]

FROM alpine:3.15 AS gemini
WORKDIR /app
COPY --from=0 /app/build/gemini ./
COPY --from=0 /app/gmi ./gmi
ENV LISTS_SUBDOMAINS=0
CMD ["./gemini"]
