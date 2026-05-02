#!/usr/bin/env bash
set -xeuo pipefail

export ZMX_SESSION_PREFIX="${ZMX_SESSION_PREFIX:-}ci-"

printf "\x1b[33mrunning ci\x1b[0m\n"

zmx run lint -d docker run -t --rm -v $(pwd):/app -w /app golangci/golangci-lint:v2.11.4 golangci-lint run
cat << EOF | zmx run tests -d
docker build -t pico-test -f ./Dockerfile.test . && \
docker run -t --rm -v $(pwd):/app pico-test
EOF
zmx wait "*"
zmx kill "*"
printf "\x1b[32msuccess tests!\x1b[0m\n"

if [ "${1:-}" != "release" ]; then
  exit 0
fi

DOCKER_TAG="latest"
DOCKER_PLATFORM="linux/amd64,linux/arm64"

docker buildx ls | grep pico || docker buildx create --name pico
docker buildx use pico
zmx run caddy -d       docker buildx build --platform "$DOCKER_PLATFORM" -t "ghcr.io/picosh/pico/caddy:$DOCKER_TAG" ./caddy
zmx run auth -d        docker buildx build --platform "$DOCKER_PLATFORM" -t "ghcr.io/picosh/pico/auth-web:$DOCKER_TAG" --build-arg APP=auth --target release-web .
zmx run cdn -d         docker buildx build --platform "$DOCKER_PLATFORM" -t "ghcr.io/picosh/pico/pgs-cdn:$DOCKER_TAG" --target release-web -f Dockerfile.cdn .
zmx run standalone -d  docker buildx build --platform "$DOCKER_PLATFORM" -t "ghcr.io/picosh/pgs:$DOCKER_TAG" --target release -f Dockerfile.standalone .
zmx run bouncer -d     docker buildx build --platform "$DOCKER_PLATFORM" -t "ghcr.io/picosh/pico/bouncer:$DOCKER_TAG" ./bouncer
zmx wait "*"

apps=("prose" "pastes" "pgs" "feeds" "pipe")
for APP in "${apps[@]}"; do
  zmx run "$APP-ssh" -d docker buildx build --platform "$DOCKER_PLATFORM" -t "ghcr.io/picosh/pico/$APP-ssh:$DOCKER_TAG" --build-arg "APP=$APP" --target release-ssh .
  zmx run "$APP-web" -d docker buildx build --platform "$DOCKER_PLATFORM" -t "ghcr.io/picosh/pico/$APP-web:$DOCKER_TAG" --build-arg "APP=$APP" --target release-web .
done
zmx wait "*"

# zmx kill "*"
printf "\x1b[32msuccess release!\x1b[0m\n"
