PGDATABASE?="pico"
PGHOST?="db"
PGUSER?="postgres"
PORT?="5432"
DB_CONTAINER?=pico-postgres-1
DOCKER_TAG?=$(shell git log --format="%H" -n 1)
DOCKER_PLATFORM?=linux/amd64,linux/arm64
DOCKER_CMD?=docker
DOCKER_BUILDX_BUILD?=$(DOCKER_CMD) buildx build --push --platform $(DOCKER_PLATFORM)

css:
	cp ./smol.css lists/public/main.css
	cp ./smol.css prose/public/main.css
	cp ./smol.css pastes/public/main.css
	cp ./smol.css imgs/public/main.css
	cp ./smol.css feeds/public/main.css

	cp ./syntax.css pastes/public/syntax.css
	cp ./syntax.css prose/public/syntax.css
.PHONY: css

lint:
	$(DOCKER_CMD) run --rm -v $(shell pwd):/app -w /app golangci/golangci-lint:latest bash -c 'apt-get update > /dev/null 2>&1 && apt-get install -y libwebp-dev > /dev/null 2>&1; golangci-lint run -E goimports -E godot --timeout 10m'
.PHONY: lint

bp-setup:
	$(DOCKER_CMD) buildx ls | grep pico || $(DOCKER_CMD) buildx create --name pico
	$(DOCKER_CMD) buildx use pico
.PHONY: bp-setup

bp-caddy: bp-setup
	$(DOCKER_BUILDX_BUILD) -t ghcr.io/picosh/pico/caddy:$(DOCKER_TAG) -f caddy/Dockerfile .
.PHONY: bp-caddy

bp-%: bp-setup
	$(DOCKER_BUILDX_BUILD) --build-arg "APP=$*" -t "ghcr.io/picosh/pico/$*-ssh:$(DOCKER_TAG)" --target release-ssh .
	$(DOCKER_BUILDX_BUILD) --build-arg "APP=$*" -t "ghcr.io/picosh/pico/$*-web:$(DOCKER_TAG)" --target release-web .
.PHONY: bp-%

bp-all: bp-prose bp-lists bp-pastes bp-imgs bp-feeds
.PHONY: bp-all

build-%:
	go build -o "build/$*-web" "./cmd/$*/web"
	go build -o "build/$*-ssh" "./cmd/$*/ssh"
.PHONY: build-%

build: build-prose build-lists build-pastes build-imgs build-feeds
.PHONY: build

format:
	go fmt ./...
.PHONY: format

create:
	$(DOCKER_CMD) exec -i $(DB_CONTAINER) psql -U $(PGUSER) < ./sql/setup.sql
.PHONY: create

teardown:
	$(DOCKER_CMD) exec -i $(DB_CONTAINER) psql -U $(PGUSER) -d $(PGDATABASE) < ./sql/teardown.sql
.PHONY: teardown

migrate:
	$(DOCKER_CMD) exec -i $(DB_CONTAINER) psql -U $(PGUSER) -d $(PGDATABASE) < ./sql/migrations/20220310_init.sql
	$(DOCKER_CMD) exec -i $(DB_CONTAINER) psql -U $(PGUSER) -d $(PGDATABASE) < ./sql/migrations/20220422_add_desc_to_user_and_post.sql
	$(DOCKER_CMD) exec -i $(DB_CONTAINER) psql -U $(PGUSER) -d $(PGDATABASE) < ./sql/migrations/20220426_add_index_for_filename.sql
	$(DOCKER_CMD) exec -i $(DB_CONTAINER) psql -U $(PGUSER) -d $(PGDATABASE) < ./sql/migrations/20220427_username_to_lower.sql
	$(DOCKER_CMD) exec -i $(DB_CONTAINER) psql -U $(PGUSER) -d $(PGDATABASE) < ./sql/migrations/20220523_timestamp_with_tz.sql
	$(DOCKER_CMD) exec -i $(DB_CONTAINER) psql -U $(PGUSER) -d $(PGDATABASE) < ./sql/migrations/20220721_analytics.sql
	$(DOCKER_CMD) exec -i $(DB_CONTAINER) psql -U $(PGUSER) -d $(PGDATABASE) < ./sql/migrations/20220722_post_hidden.sql
	$(DOCKER_CMD) exec -i $(DB_CONTAINER) psql -U $(PGUSER) -d $(PGDATABASE) < ./sql/migrations/20220727_post_change_post_contraints.sql
	$(DOCKER_CMD) exec -i $(DB_CONTAINER) psql -U $(PGUSER) -d $(PGDATABASE) < ./sql/migrations/20220730_post_change_filename_to_slug.sql
	$(DOCKER_CMD) exec -i $(DB_CONTAINER) psql -U $(PGUSER) -d $(PGDATABASE) < ./sql/migrations/20220801_add_post_tags.sql
	$(DOCKER_CMD) exec -i $(DB_CONTAINER) psql -U $(PGUSER) -d $(PGDATABASE) < ./sql/migrations/20220811_add_data_to_post.sql
	$(DOCKER_CMD) exec -i $(DB_CONTAINER) psql -U $(PGUSER) -d $(PGDATABASE) < ./sql/migrations/20220811_add_feature.sql
	$(DOCKER_CMD) exec -i $(DB_CONTAINER) psql -U $(PGUSER) -d $(PGDATABASE) < ./sql/migrations/20221108_add_expires_at_to_posts.sql
	$(DOCKER_CMD) exec -i $(DB_CONTAINER) psql -U $(PGUSER) -d $(PGDATABASE) < ./sql/migrations/20221112_add_feeds_space.sql
	$(DOCKER_CMD) exec -i $(DB_CONTAINER) psql -U $(PGUSER) -d $(PGDATABASE) < ./sql/migrations/20230310_add_aliases_table.sql
	$(DOCKER_CMD) exec -i $(DB_CONTAINER) psql -U $(PGUSER) -d $(PGDATABASE) < ./sql/migrations/20230326_add_feed_items.sql
.PHONY: migrate

latest:
	$(DOCKER_CMD) exec -i $(DB_CONTAINER) psql -U $(PGUSER) -d $(PGDATABASE) < ./sql/migrations/20230326_add_feed_items.sql
.PHONY: latest

psql:
	$(DOCKER_CMD) exec -it $(DB_CONTAINER) psql -U $(PGUSER) -d $(PGDATABASE)
.PHONY: psql

dump:
	$(DOCKER_CMD) exec -it $(DB_CONTAINER) pg_dump -U $(PGUSER) $(PGDATABASE) > ./backup.sql
.PHONY: dump

restore:
	$(DOCKER_CMD) cp ./backup.sql $(DB_CONTAINER):/backup.sql
	$(DOCKER_CMD) exec -it $(DB_CONTAINER) /bin/bash
	# psql postgres -U postgres -d pico < /backup.sql
.PHONY: restore
