# pico.sh - hacker labs

> [!IMPORTANT]\
> Read our docs at [pico.sh](https://pico.sh).

Open source and managed web services leveraging SSH.

The secret ingredient to all our services is how we let users publish content
without needing to install anything. We accomplish this with the SSH tools you
already have installed on your system.

Want to publish a blog post? Use rsync, scp, or sftp. Want to publish a website?
Use rsync, scp, or sftp. Want to share a code snippet with a colleague? Use
rsync, scp, or sftp. Hopefully you see the trend.

- [pgs.sh](https://pico.sh/pgs): A static site hosting platform using SSH for
  site deployments.
- [tuns.sh](https://pico.sh/tuns): https/wss/tcp tunnels to localhost using only
  SSH.
- [imgs.sh](https://pico.sh/imgs): Docker image registry using SSH for
  authentication.
- [prose.sh](https://prose.sh): A blog platform using SSH for content
  management.
- [pastes.sh](https://pastes.sh): Upload code snippets using rsync, scp, and
  sftp.
- [feeds.sh](https://feeds.sh): An RSS email notification service using SSH.

## Deploy a site with a single command

Upload your static site to us:

```bash
rsync -rv ./public/ pgs.sh:/mysite/
```

Now your site is available with TLS handled for you:
https://{user}-mysite.pgs.sh We also automatically handle TLS for your custom
domains!

## Access localhost using https

if you have a local webserver on localhost:8000, activate an SSH tunnel to us:

```bash
ssh -R dev:80:localhost:8000 tuns.sh
```

Now your local dev server is availble on the web: https://dev.tuns.sh

## Publish blog articles with a single command

Create your first post, (e.g. `hello-world.md`):

```md
# hello world!

This is my first blog post.

Cya!
```

Upload the post to us:

```bash
scp hello-world.md prose.sh:/
```

Congrats! You just published a blog article, accessible here:
https://{user}.prose.sh/hello-world

## Push and pull docker images using SSH

Open a tunnel to our docker registry:

```bash
ssh -L 1338:localhost:80 -N imgs.sh
```

Now you are authenticated! You are now able to push and pull like normal:

```bash
docker push localhost:1338/alpine:latest
docker pull localhost:1338/alpine:latest
```

All images sent to us are private and scoped to your user automatically.

## Easily share code snippets

Pipe some stdout to us:

```bash
git diff | ssh pastes.sh changes.patch
```

And instantly share your code snippets: https://{user}.pastes.sh/changes.patch

## Receive email notifications for your favorite rss feeds

Create a blogs.txt file:

```
=: email rss@myemail.com
=: digest_interval 1day
=> https://pico.prose.sh/rss
=> https://erock.prose.sh/rss
```

Then upload it to us:

```bash
scp blogs.txt feeds.sh:/
```

After the daily interval has been reached, you will receive an email with your
feeds!

## Ready to join?

https://pico.sh/getting-started
