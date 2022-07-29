DOCKER_TAG?=$(shell git log --format="%H" -n 1)

bp-setup:
	docker buildx ls | grep pico || docker buildx create --name pico
	docker buildx use pico
.PHONY: bp-setup

bp-ssh: bp-setup
	docker buildx build --push --platform linux/amd64,linux/arm64 -t neurosnap/pastes-ssh:$(DOCKER_TAG) --target ssh -f Dockerfile ..
.PHONY: bp-ssh

bp-web: bp-setup
	docker buildx build --push --platform linux/amd64,linux/arm64 -t neurosnap/pastes-web:$(DOCKER_TAG) --target web -f Dockerfile ..
.PHONY: bp-web

bp: bp-ssh bp-web
.PHONY: bp
