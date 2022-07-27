PGDATABASE?="pico"
PGHOST?="db"
PGUSER?="postgres"
PORT?="5432"
DB_CONTAINER?=pico-services_db_1

test:
	docker run --rm -v $(shell pwd):/app -w /app golangci/golangci-lint:latest golangci-lint run -E goimports -E godot
.PHONY: test

build:
	go build -o build/web ./cmd/web
	go build -o build/ssh ./cmd/ssh
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
.PHONY: migrate

latest:
	docker exec -i $(DB_CONTAINER) psql -U $(PGUSER) -d $(PGDATABASE) < ./db/migrations/20220727_post_change_post_contraints.sql
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
	# psql postgres -U postgres < /backup.sql
.PHONY: restore

bp-caddy:
	docker build -t neurosnap/prose-caddy -f Dockerfile.caddy .
	docker push neurosnap/prose-caddy
.PHONY: bp-caddy

bp-ssh:
	docker build -t neurosnap/prose-ssh --target ssh .
	docker push neurosnap/prose-ssh
.PHONY: bp-ssh

bp-web:
	docker build -t neurosnap/prose-web --target web .
	docker push neurosnap/prose-web
.PHONY: bp-web

bp: bp-ssh bp-web bp-caddy
.PHONY: bp

deploy:
	docker system prune -f
	docker-compose -f production.yml pull --ignore-pull-failures
	docker-compose -f production.yml up --no-deps -d
.PHONY: deploy
