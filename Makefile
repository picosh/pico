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

smol:
	curl https://pico.sh/smol.css -o ./pkg/apps/prose/public/smol-v2.css
	cat ./pkg/apps/prose/artifacts/main.css >> ./pkg/apps/prose/public/smol-v2.css
	curl https://pico.sh/smol.css -o ./pkg/apps/pastes/public/smol.css
.PHONY: smol

css:
	cp ./syntax.css ./pkg/apps/pastes/public/syntax.css
	cp ./syntax.css ./pkg/apps/prose/public/syntax.css
.PHONY: css

lint:
	golangci-lint run
.PHONY: lint

test:
	go test ./...
.PHONY: test

snaps:
	UPDATE_SNAPS=true go test ./...
.PHONY: snaps

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

bp-pico: bp-setup
	$(DOCKER_BUILDX_BUILD) -t ghcr.io/picosh/pico/pico-ssh:$(DOCKER_TAG) --build-arg APP=pico --target release-ssh .
.PHONY: bp-auth

bp-bouncer: bp-setup
	$(DOCKER_BUILDX_BUILD) -t ghcr.io/picosh/pico/bouncer:$(DOCKER_TAG) ./bouncer
.PHONY: bp-bouncer

bp-ssh-%: bp-setup
	$(DOCKER_BUILDX_BUILD) --build-arg "APP=$*" -t "ghcr.io/picosh/pico/$*-ssh:$(DOCKER_TAG)" --target release-ssh .
.PHONY: pgs-ssh

bp-%: bp-setup
	$(DOCKER_BUILDX_BUILD) --build-arg "APP=$*" -t "ghcr.io/picosh/pico/$*-ssh:$(DOCKER_TAG)" --target release-ssh .
	$(DOCKER_BUILDX_BUILD) --build-arg "APP=$*" -t "ghcr.io/picosh/pico/$*-web:$(DOCKER_TAG)" --target release-web .
.PHONY: bp-%

bp-all: bp-prose bp-pastes bp-feeds bp-pgs bp-auth bp-bouncer bp-pipe
.PHONY: bp-all

build-auth:
	go build -o "build/auth" "./cmd/auth/web"
.PHONY: build-auth

build-pico:
	go build -o "build/pico-ssh" "./cmd/pico/ssh"
.PHONY: build-auth

build-%:
	go build -o "build/$*-web" "./cmd/$*/web"
	go build -o "build/$*-ssh" "./cmd/$*/ssh"
.PHONY: build-%

build: build-prose build-pastes build-feeds build-pgs build-auth build-pico build-pipe
.PHONY: build

scripts:
	# might need to set MINIO_URL
	docker run --rm -it --env-file .env -v $(shell pwd):/app -w /app golang:1.24 /bin/bash
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
	$(DOCKER_CMD) exec -i $(DB_CONTAINER) psql -U $(PGUSER) -d $(PGDATABASE) < ./sql/migrations/20240324_add_analytics_table.sql
	$(DOCKER_CMD) exec -i $(DB_CONTAINER) psql -U $(PGUSER) -d $(PGDATABASE) < ./sql/migrations/20240819_add_projects_blocked.sql
	$(DOCKER_CMD) exec -i $(DB_CONTAINER) psql -U $(PGUSER) -d $(PGDATABASE) < ./sql/migrations/20241028_add_analytics_indexes.sql
	$(DOCKER_CMD) exec -i $(DB_CONTAINER) psql -U $(PGUSER) -d $(PGDATABASE) < ./sql/migrations/20241114_add_namespace_to_analytics.sql
	$(DOCKER_CMD) exec -i $(DB_CONTAINER) psql -U $(PGUSER) -d $(PGDATABASE) < ./sql/migrations/20241125_add_content_type_to_analytics.sql
	$(DOCKER_CMD) exec -i $(DB_CONTAINER) psql -U $(PGUSER) -d $(PGDATABASE) < ./sql/migrations/20241202_add_more_idx_analytics.sql
	$(DOCKER_CMD) exec -i $(DB_CONTAINER) psql -U $(PGUSER) -d $(PGDATABASE) < ./sql/migrations/20250319_add_tuns_event_logs_table.sql
	$(DOCKER_CMD) exec -i $(DB_CONTAINER) psql -U $(PGUSER) -d $(PGDATABASE) < ./sql/migrations/20250320_add_tunnel_id_to_tuns_event_logs_table.sql
	$(DOCKER_CMD) exec -i $(DB_CONTAINER) psql -U $(PGUSER) -d $(PGDATABASE) < ./sql/migrations/20250410_add_index_analytics_visits_host_list.sql
	$(DOCKER_CMD) exec -i $(DB_CONTAINER) psql -U $(PGUSER) -d $(PGDATABASE) < ./sql/migrations/20250418_add_project_post_idx_analytics.sql
.PHONY: migrate

latest:
	$(DOCKER_CMD) exec -i $(DB_CONTAINER) psql -U $(PGUSER) -d $(PGDATABASE) < ./sql/migrations/20250418_add_project_post_idx_analytics.sql
.PHONY: latest

psql:
	$(DOCKER_CMD) exec -it $(DB_CONTAINER) psql -U $(PGUSER) -d $(PGDATABASE)
.PHONY: psql

dump:
	$(DOCKER_CMD) exec $(DB_CONTAINER) pg_dump -U $(PGUSER) $(PGDATABASE) > ./backup.sql
.PHONY: dump

restore:
	$(DOCKER_CMD) cp ./backup.sql $(DB_CONTAINER):/backup.sql
	$(DOCKER_CMD) exec -it $(DB_CONTAINER) /bin/bash
	# psql postgres -U postgres -d pico < /backup.sql
.PHONY: restore
