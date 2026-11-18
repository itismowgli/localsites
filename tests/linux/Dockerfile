# A clean Ubuntu 24.04 with Homebrew, the way LocalSites sets it up on Linux: only the few system tools
# Homebrew needs (no compiler), then prebuilt packages for Apache, MySQL 8.4 and PHP.
#   docker build -t localsites-linux tests/linux
FROM ubuntu:24.04
ARG DEBIAN_FRONTEND=noninteractive
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates curl git file procps sudo locales \
    && rm -rf /var/lib/apt/lists/* && locale-gen en_US.UTF-8
RUN useradd -m -s /bin/bash dev && echo 'dev ALL=(ALL) NOPASSWD:ALL' > /etc/sudoers.d/dev
USER dev
WORKDIR /home/dev
ENV HOMEBREW_NO_AUTO_UPDATE=1 HOMEBREW_NO_INSTALL_CLEANUP=1 HOMEBREW_NO_ENV_HINTS=1 NONINTERACTIVE=1 LANG=en_US.UTF-8
RUN /bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"
ENV PATH=/home/linuxbrew/.linuxbrew/bin:/home/linuxbrew/.linuxbrew/sbin:$PATH
RUN brew install httpd mysql@8.4
# The PHP tap is trusted as a whole: its formulas depend on each other (php@8.5 loads shivammathur/php/php)
RUN brew tap shivammathur/php && (brew trust shivammathur/php 2>/dev/null || true)
ARG PHP_VERSIONS="8.5 7.4"
RUN for v in $PHP_VERSIONS; do brew install shivammathur/php/php@$v || exit 1; done && du -sh /home/linuxbrew/.linuxbrew
# Only for the tests: fixtures use sqlite3 and a small Python server
USER root
RUN apt-get update && apt-get install -y --no-install-recommends python3 sqlite3 && rm -rf /var/lib/apt/lists/*
USER dev
