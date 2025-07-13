# self-host guide

`pico` as a platform is not designed to be self-hosted, even though all the code
is open and lives inside this repo. We has no issues with self-hosting this
platform in its entirety but it is simply not a design goal or focus for us.

Having said that, we do have components of this platform that we do officially
support for self-hosting.

# tuns.sh

Our tunneling service is simply a managed `sish` service that has some features
related to pico authentication and authorization.

`sish` is a stateless go binary that can be completely configured from a single
command.

We have comprehensive `sish` docs here: https://docs.ssi.sh/

# pgs.sh

Our static site hosting service has a dedicated docs repo that contains all the
information you need to self-host pgs here: https://github.com/picosh/pgs/

`pgs` requires extra dependencies primarily for generating TLS certificates
on-the-fly.
