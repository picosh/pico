# pico.sh - hacker labs

Open source and managed services leveraging SSH.

The secret ingredient to all our services is how we let users
publish changes to their blog and sites without needing to install anything.
We accomplish this with what is colloquially termed SSH Apps. By using
the SSH protocol and golang's implementation of SSH, we can create
golang binaries that interface with SSH in unique ways.

Want to publish a blog post? Use rsync, scp, or sftp.  Want to publish a
website?  Use rsync, scp, or sftp.  Want to share a code snippet with a
colleague?  Use rsync, scp, or sftp.  Hopefully you see the trend.

- pgs.sh: A static site hosting platform using SSH.
- tuns.sh:  HTTP(S)/WS(S)/TCP Tunnels to localhost using only SSH.
- imgs.sh: Docker image registry using SSH for authentication.
- prose.sh: A blog platform using SSH for content management.
- pastes.sh: Upload code snippets using SSH.
- feeds.sh: An RSS email notification system using SSH.

Read our docs at [pico.sh](https://pico.sh).

## development

[Local setup](/dev.md)
