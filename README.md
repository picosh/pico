# pico services

This repo hosts the following pico services:

- [prose.sh](https://prose.sh)
- [lists.sh](https://lists.sh)
- [pastes.sh](https://pastes.sh)

## comms

- [website](https://pico.sh)
- [irc #pico.sh](irc://irc.libera.chat/#pico.sh)
- [mailing list](https://lists.sr.ht/~erock/pico.sh)
- [ticket tracker](https://github.com/picosh/pico/issues)
- [email](mailto:hello@pico.sh)

## development

- `golang` >= 1.19
- `direnv` to load environment vars

```bash
cp ./.env.example .env
```

Boot up database

```bash
docker compose up --profile db -d
```

Create db and migrate

```bash
make create
make migrate
```

Build services

```bash
make build
```

All services are built inside the `./build` folder.

If you want to start prose:

```bash
./build/prose-web
# in a separate terminal
./build/prose-ssh
```

## deployment

We use an image based deployment, so all of our images are uploaded to
[hub.docker.com](https://hub.docker.com/u/neurosnap)

```bash
DOCKER_TAG=latest make bp-all
```

Once images are built, docker compose is used to stand up the services:

```bash
docker compose up -d
```

This makes use of a production `.env.prod` environment file which defines
the various listening addresses and services that will be started. For production,
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

For any migrations, logging into the our database server, pulling the changes
to migrations and running `make latest` is all that is needed.
