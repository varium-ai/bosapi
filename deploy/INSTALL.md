# Install from source

Obtain a reviewed community source checkout. With Docker, Compose v2, Buildx,
Bash, OpenSSL and curl available, run `./install.sh` from that checkout. The wrapper
invokes `./deploy/stack.sh up --source`; it does not clone a private repository,
install Docker, use SSH, or change system packages.

For configuration, HTTPS, monitoring, upgrades and backups, follow
[the deployment guide](README.md) and [the project README](../README.md).
