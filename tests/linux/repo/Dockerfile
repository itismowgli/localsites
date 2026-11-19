# Tools to build the signed apt and dnf repositories for LocalSites (run by `go run ./tools/release repos`,
# and once by `make package-key` to create the signing key). The key arrives on stdin and is only kept in a
# temporary GnuPG folder inside the container.
FROM debian:12-slim
ARG DEBIAN_FRONTEND=noninteractive
RUN apt-get update && apt-get install -y --no-install-recommends gnupg dpkg-dev apt-utils createrepo-c rpm ca-certificates \
    && rm -rf /var/lib/apt/lists/*
COPY build-repos.sh new-key.sh /usr/local/bin/
