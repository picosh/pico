# pico services

This repo hosts the following pico services:

- [prose.sh](https://prose.sh)
- [lists.sh](https://lists.sh)
- [pastes.sh](https://pastes.sh)

## comms

- [website](https://pico.sh)
- [irc #pico.sh](irc://irc.libera.chat/#pico.sh)
- [mailing list](https://lists.sr.ht/~erock/pico.sh)
- [ticket tracker](https://todo.sr.ht/~erock/pico.sh)
- [email](mailto:hello@pico.sh)

## development

- `golang` >= 1.18
- `direnv` to load environment vars 

```bash
cp ./.env.example .env
```

Boot up database

```bash
docker compose up -d
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

Then ssh into the production server and run:

```bash
./start.sh pull
./start.sh
```

For any migrations, right dropping into `psql` on our production database and
pasting the SQL.  This process is a WIP and will update over time.
