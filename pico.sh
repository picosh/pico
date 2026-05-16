#!/usr/bin/env bash
set -euo pipefail

export ZMX_SESSION_PREFIX="${ZMX_SESSION_PREFIX:-ci.pico.}"
JOB_ID="${PICO_CI_JOB_ID:-local}"
EVENT_TYPE="${PICO_CI_EVENT_TYPE:-manual}"

printf "\x1b[33m[%s] running ci (event=%s)\x1b[0m\n" "$JOB_ID" "$EVENT_TYPE"

zmx run lint -d  docker run -t --rm -v $(pwd):/app -w /app golangci/golangci-lint:v2.11.4 golangci-lint run
zmx run build -d docker build -t pico-test -f ./Dockerfile.test . \
                 docker run -t --rm pico-test
zmx wait "*"
printf "\x1b[32msuccess tests!\x1b[0m\n"

if [ "$EVENT_TYPE" != "release" ]; then
  exit 0
fi

DOCKER_TAG="latest"
DOCKER_PLATFORM="linux/amd64,linux/arm64"

docker buildx ls | grep pico || docker buildx create --name pico
docker buildx use pico
zmx run caddy -d       docker buildx build --push --platform "$DOCKER_PLATFORM" -t "ghcr.io/picosh/pico/caddy:$DOCKER_TAG" ./caddy
zmx run bouncer        docker buildx build --push --platform "$DOCKER_PLATFORM" -t "ghcr.io/picosh/pico/bouncer:$DOCKER_TAG" ./bouncer
zmx wait "*"

zmx run auth        docker buildx build --push --platform "$DOCKER_PLATFORM" -t "ghcr.io/picosh/pico/auth-web:$DOCKER_TAG" --build-arg APP=auth --target release-web .
zmx run cdn         docker buildx build --push --platform "$DOCKER_PLATFORM" -t "ghcr.io/picosh/pico/pgs-cdn:$DOCKER_TAG" --target release-web -f Dockerfile.cdn .
zmx run standalone  docker buildx build --push --platform "$DOCKER_PLATFORM" -t "ghcr.io/picosh/pgs:$DOCKER_TAG" --target release -f Dockerfile.standalone .
zmx wait "*"

apps=("prose" "pastes" "pgs" "feeds" "pipe")
for APP in "${apps[@]}"; do
  zmx run "$APP-ssh" -d docker buildx build --push --platform "$DOCKER_PLATFORM" -t "ghcr.io/picosh/pico/$APP-ssh:$DOCKER_TAG" --build-arg "APP=$APP" --target release-ssh .
  zmx run "$APP-web" -d docker buildx build --push --platform "$DOCKER_PLATFORM" -t "ghcr.io/picosh/pico/$APP-web:$DOCKER_TAG" --build-arg "APP=$APP" --target release-web .
  zmx wait "*"
done

printf "\x1b[32msuccess release!\x1b[0m\n"
