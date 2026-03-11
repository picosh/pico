#!/bin/bash
set -xeuo pipefail

DOCKER_TAG="latest"

zmx run tests podman compose -f docker-compose.test.yml up
zmx wait

zmx run caddy 			podman build --tag "ghcr.io/picosh/pico/caddy:$DOCKER_TAG" ./caddy
zmx run auth 				podman build --tag "ghcr.io/picosh/pico/auth-web:$DOCKER_TAG" --build-arg APP=auth --target release-web .
zmx run cdn 				podman build --tag "ghcr.io/picosh/pico/pgs-cdn:$DOCKER_TAG" --target release-web -f Dockerfile.cdn .
zmx run standalone 	podman build --tag "ghcr.io/picosh/pgs:$DOCKER_TAG" --target release -f Dockerfile.standalone .
zmx run bouncer 		podman build --tag "ghcr.io/picosh/pico/bouncer:$DOCKER_TAG" ./bouncer
zmx wait

apps=("prose" "pastes" "pgs" "feeds" "pipe")
for APP in "${apps[@]}"; do
  zmx run $APP-ssh podman build --tag "ghcr.io/picosh/pico/$APP-ssh:$DOCKER_TAG" --build-arg "APP=$APP" --target release-ssh .
  zmx run $APP-web podman build --tag "ghcr.io/picosh/pico/$APP-web:$DOCKER_TAG" --build-arg "APP=$APP" --target release-web .
done
