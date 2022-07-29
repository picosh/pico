PGDATABASE?="pico"
PGHOST?="db"
PGUSER?="postgres"
PORT?="5432"
DB_CONTAINER?=pico-services_db_1
DOCKER_TAG?=$(shell git log --format="%H" -n 1)

test:
	docker run --rm -v $(shell pwd):/app -w /app golangci/golangci-lint:latest golangci-lint run -E goimports -E godot
.PHONY: test

bp-setup:
	docker buildx ls | grep pico || docker buildx create --name pico
	docker buildx use pico
.PHONY: bp-setup

bp-caddy: bp-setup
	docker buildx build --push --platform linux/amd64,linux/arm64 -t neurosnap/cloudflare-caddy:$(DOCKER_TAG) -f Dockerfile.caddy .
.PHONY: bp-caddy

bp-prose:
	$(MAKE) -C prose bp
.PHONY: bp-prose

bp-pastes:
	$(MAKE) -C pastes bp
.PHONY: bp-pastes

bp-lists:
	$(MAKE) -C lists bp
.PHONY: bp-lists

bp-all: bp-prose bp-lists bp-pastes
.PHONY: bp-all

build-prose:
	go build -o build/prose-web ./cmd/prose/web
	go build -o build/prose-ssh ./cmd/prose/ssh
.PHONY: build-prose

build-lists:
	go build -o build/lists-web ./cmd/lists/web
	go build -o build/lists-ssh ./cmd/lists/ssh
	go build -o build/lists-gemini ./cmd/lists/gemini
.PHONY: build-lists

build-pastes:
	go build -o build/pastes-web ./cmd/pastes/web
	go build -o build/pastes-ssh ./cmd/pastes/ssh
.PHONY: build-pastes

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
.PHONY: migrate

latest:
	docker exec -i $(DB_CONTAINER) psql -U $(PGUSER) -d $(PGDATABASE) < ./db/migrations/20220722_post_hidden.sql
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
