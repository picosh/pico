PGDATABASE?="pico"
PGHOST?="db"
PGUSER?="postgres"
PORT?="5432"
DB_CONTAINER?=pico-postgres-1
DOCKER_TAG?=$(shell git log --format="%H" -n 1)
DOCKER_PLATFORM?=linux/amd64,linux/arm64
DOCKER_BUILDX_BUILD?=docker buildx build --push --platform $(DOCKER_PLATFORM)

lint:
	docker run --rm -v $(shell pwd):/app -w /app golangci/golangci-lint:latest golangci-lint run -E goimports -E godot
.PHONY: lint

bp-setup:
	docker buildx ls | grep pico || docker buildx create --name pico
	docker buildx use pico
.PHONY: bp-setup

bp-caddy: bp-setup
	$(DOCKER_BUILDX_BUILD) -t neurosnap/pico-caddy:$(DOCKER_TAG) -f caddy/Dockerfile .
.PHONY: bp-caddy

bp-%: bp-setup
	$(DOCKER_BUILDX_BUILD) --build-arg "APP=$*" -t "neurosnap/$*-ssh:$(DOCKER_TAG)" --target release-ssh .
	$(DOCKER_BUILDX_BUILD) --build-arg "APP=$*" -t "neurosnap/$*-web:$(DOCKER_TAG)" --target release-web .
	[[ "$*" == "lists" ]] && $(DOCKER_BUILDX_BUILD) --build-arg "APP=$*" -t "neurosnap/$*-gemini:$(DOCKER_TAG)" --target release-gemini . || true
.PHONY: bp-%

bp-all: bp-prose bp-lists bp-pastes
.PHONY: bp-all

build-%:
	go build -o "build/$*-web" "./cmd/$*/web"
	go build -o "build/$*-ssh" "./cmd/$*/ssh"
	[[ "$*" == "lists" ]] && go build -o "build/$*-gemini" "./cmd/$*/gemini" || true
.PHONY: build-%

build: build-prose build-lists build-pastes
.PHONY: build

format:
	go fmt ./...
.PHONY: format

create:
	docker exec -i $(DB_CONTAINER) psql -U $(PGUSER) < ./db/setup.sql
.PHONY: create

teardown:
	docker exec -i $(DB_CONTAINER) psql -U $(PGUSER) -d $(PGDATABASE) < ./db/teardown.sql
.PHONY: teardown

migrate:
	docker exec -i $(DB_CONTAINER) psql -U $(PGUSER) -d $(PGDATABASE) < ./db/migrations/20220310_init.sql
	docker exec -i $(DB_CONTAINER) psql -U $(PGUSER) -d $(PGDATABASE) < ./db/migrations/20220422_add_desc_to_user_and_post.sql
	docker exec -i $(DB_CONTAINER) psql -U $(PGUSER) -d $(PGDATABASE) < ./db/migrations/20220426_add_index_for_filename.sql
	docker exec -i $(DB_CONTAINER) psql -U $(PGUSER) -d $(PGDATABASE) < ./db/migrations/20220427_username_to_lower.sql
	docker exec -i $(DB_CONTAINER) psql -U $(PGUSER) -d $(PGDATABASE) < ./db/migrations/20220523_timestamp_with_tz.sql
	docker exec -i $(DB_CONTAINER) psql -U $(PGUSER) -d $(PGDATABASE) < ./db/migrations/20220721_analytics.sql
	docker exec -i $(DB_CONTAINER) psql -U $(PGUSER) -d $(PGDATABASE) < ./db/migrations/20220722_post_hidden.sql
	docker exec -i $(DB_CONTAINER) psql -U $(PGUSER) -d $(PGDATABASE) < ./db/migrations/20220727_post_change_post_contraints.sql
	docker exec -i $(DB_CONTAINER) psql -U $(PGUSER) -d $(PGDATABASE) < ./db/migrations/20220730_post_change_filename_to_slug.sql
	docker exec -i $(DB_CONTAINER) psql -U $(PGUSER) -d $(PGDATABASE) < ./db/migrations/20220801_add_post_tags.sql
.PHONY: migrate

latest:
	docker exec -i $(DB_CONTAINER) psql -U $(PGUSER) -d $(PGDATABASE) < ./db/migrations/20220801_add_post_tags.sql
.PHONY: latest

psql:
	docker exec -it $(DB_CONTAINER) psql -U $(PGUSER)
.PHONY: psql

dump:
	docker exec -it $(DB_CONTAINER) pg_dump -U $(PGUSER) $(PGDATABASE) > ./backup.sql
.PHONY: dump

restore:
	docker cp ./backup.sql $(DB_CONTAINER):/backup.sql
	docker exec -it $(DB_CONTAINER) /bin/bash
	# psql postgres -U postgres -d pico < /backup.sql
.PHONY: restore
