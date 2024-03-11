PGDATABASE?="pico"
PGHOST?="db"
PGUSER?="postgres"
PORT?="5432"
DB_CONTAINER?=pico-postgres-1
DOCKER_TAG?=$(shell git log --format="%H" -n 1)
DOCKER_PLATFORM?=linux/amd64,linux/arm64
DOCKER_CMD?=docker
DOCKER_BUILDX_BUILD?=$(DOCKER_CMD) buildx build --push --platform $(DOCKER_PLATFORM)
WRITE?=0

css:
	cp ./syntax.css pastes/public/syntax.css
	cp ./syntax.css prose/public/syntax.css
.PHONY: css

lint:
	$(DOCKER_CMD) run --rm -v $(shell pwd):/app -w /app golangci/golangci-lint:latest run -E goimports -E godot --timeout 10m
.PHONY: lint

lint-dev:
	golangci-lint run -E goimports -E godot --timeout 10m
.PHONY: lint-dev

bp-setup:
	$(DOCKER_CMD) buildx ls | grep pico || $(DOCKER_CMD) buildx create --name pico
	$(DOCKER_CMD) buildx use pico
.PHONY: bp-setup

bp-caddy: bp-setup
	$(DOCKER_BUILDX_BUILD) -t ghcr.io/picosh/pico/caddy:$(DOCKER_TAG) ./caddy
.PHONY: bp-caddy

bp-auth: bp-setup
	$(DOCKER_BUILDX_BUILD) -t ghcr.io/picosh/pico/auth-web:$(DOCKER_TAG) --build-arg APP=auth --target release-web .
.PHONY: bp-auth

bp-bouncer: bp-setup
	$(DOCKER_BUILDX_BUILD) -t ghcr.io/picosh/pico/bouncer:$(DOCKER_TAG) ./bouncer
.PHONY: bp-bouncer

bp-%: bp-setup
	$(DOCKER_BUILDX_BUILD) --build-arg "APP=$*" -t "ghcr.io/picosh/pico/$*-ssh:$(DOCKER_TAG)" --target release-ssh .
	$(DOCKER_BUILDX_BUILD) --build-arg "APP=$*" -t "ghcr.io/picosh/pico/$*-web:$(DOCKER_TAG)" --target release-web .
.PHONY: bp-%

bp-all: bp-prose bp-pastes bp-imgs bp-feeds bp-pgs bp-auth bp-bouncer
.PHONY: bp-all

bp-podman-%:
	$(DOCKER_CMD) buildx build --platform $(DOCKER_PLATFORM) --build-arg "APP=$*" -t "ghcr.io/picosh/pico/$*-ssh:$(DOCKER_TAG)" --target release-ssh .
	$(DOCKER_CMD) buildx build --platform $(DOCKER_PLATFORM) --build-arg "APP=$*" -t "ghcr.io/picosh/pico/$*-web:$(DOCKER_TAG)" --target release-web .
	$(DOCKER_CMD) push "ghcr.io/picosh/pico/$*-ssh:$(DOCKER_TAG)"
	$(DOCKER_CMD) push "ghcr.io/picosh/pico/$*-web:$(DOCKER_TAG)"
.PHONY: bp-%

bp-podman-all: bp-podman-prose bp-podman-pastes bp-podman-imgs bp-podman-feeds bp-podman-pgs
.PHONY: all

build-auth:
	go build -o "build/auth" "./cmd/auth/web"
.PHONY: build-auth

build-%:
	go build -o "build/$*-web" "./cmd/$*/web"
	go build -o "build/$*-ssh" "./cmd/$*/ssh"
.PHONY: build-%

build: build-prose build-pastes build-imgs build-feeds build-pgs build-auth
.PHONY: build

store-clean:
	WRITE=$(WRITE) go run ./cmd/scripts/clean-object-store/clean.go
.PHONY: store-clean

pico-plus:
	# USER=picouser PTYPE=stripe TXID=pi_xxx make pico-plus
	# USER=picouser PTYPE=snail make pico-plus
	# USER=picouser make pico-plus
	go run ./cmd/scripts/pico-plus/main.go $(USER) $(PTYPE) $(TXID)
.PHONY: pico-plus

scripts:
	# might need to set MINIO_URL
	docker run --rm -it --env-file .env -v $(shell pwd):/app -w /app golang:1.22 /bin/bash
.PHONY: scripts

fmt:
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
	$(DOCKER_CMD) exec -i $(DB_CONTAINER) psql -U $(PGUSER) -d $(PGDATABASE) < ./sql/migrations/20230707_add_projects_table.sql
	$(DOCKER_CMD) exec -i $(DB_CONTAINER) psql -U $(PGUSER) -d $(PGDATABASE) < ./sql/migrations/20230921_add_tokens_table.sql
	$(DOCKER_CMD) exec -i $(DB_CONTAINER) psql -U $(PGUSER) -d $(PGDATABASE) < ./sql/migrations/20240120_add_payment_history.sql
	$(DOCKER_CMD) exec -i $(DB_CONTAINER) psql -U $(PGUSER) -d $(PGDATABASE) < ./sql/migrations/20240221_add_project_acl.sql
	$(DOCKER_CMD) exec -i $(DB_CONTAINER) psql -U $(PGUSER) -d $(PGDATABASE) < ./sql/migrations/20240311_add_public_key_name.sql
.PHONY: migrate

latest:
	$(DOCKER_CMD) exec -i $(DB_CONTAINER) psql -U $(PGUSER) -d $(PGDATABASE) < ./sql/migrations/20240311_add_public_key_name.sql
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
