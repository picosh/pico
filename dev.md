## development

- `golang` >= 1.23.1
- `direnv` to load environment vars

```bash
cp ./.env.example .env
```

If you are running apps outside of docker, remember to change the postgres, minio, and imgproxy hostnames to "localhost" in `.env`.

Initialize local env variables using direnv

```bash
echo dotenv > .envrc && direnv allow
```

Boot up database (or bring your own)

```bash
docker compose -f docker-compose.yml -f docker-compose.override.yml --profile db up -d
```

Create db and migrate

```bash
make create
make migrate
```

```bash
go run ./cmd/pgs/ssh
# in a separate terminal
go run ./cmd/pgs/web
```

## sign up and upload files

The initial database has no users, you need to sign up via pico/ssh:

```bash
go run ./cmd/pico/ssh
# in a separate terminal, complete the signup flow, set your username to "picouser"
ssh localhost -p 2222
```

Stop the pico SSH server, then you can upload files:

```bash
go run ./cmd/pgs/ssh
# in a separate terminal
go run ./cmd/pgs/web
# in a third terminal
echo 'Hello, World!' > file.txt
scp -P 2222 file.txt localhost:/test/file.txt
curl -iH "Host: picouser-test.pgs.dev.pico.sh" localhost:3000/file.txt
```

## deployment

We use an image based deployment, so all of our images are uploaded to
[ghcr.io/picosh/pico](https://github.com/picosh/pico/packages)

```bash
DOCKER_TAG=latest make bp-all
```

Once images are built, docker compose is used to stand up the services:

```bash
docker compose up -d
```

This makes use of a production `.env.prod` environment file which defines the
various listening addresses and services that will be started. For production,
we add a `.envrc` containing the following:

```bash
export COMPOSE_FILE=docker-compose.yml:docker-compose.prod.yml
export COMPOSE_PROFILES=services,caddy
```

And symlink `.env` to `.env.prod`:

```bash
ln -s .env.prod .env
```

This allows us to use docker-compose normally as we would in development.

For any migrations, logging into the our database server, pulling the changes to
migrations and running `make latest` is all that is needed.
