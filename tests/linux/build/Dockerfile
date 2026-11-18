# Builds the LocalSites desktop app for Linux (Wails on GTK 3 and WebKitGTK 4.1), as Ubuntu 24.04 ships them.
#   docker build -t localsites-linux-build tests/linux/build
#   docker run --rm -v "$PWD":/src -w /src localsites-linux-build make linux-packages VERSION=…
FROM ubuntu:24.04
ARG DEBIAN_FRONTEND=noninteractive
ARG GO_VERSION=1.27.3
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates curl git build-essential pkg-config \
      libgtk-3-dev libwebkit2gtk-4.1-dev make file imagemagick \
    && rm -rf /var/lib/apt/lists/*
RUN arch=$(dpkg --print-architecture) && curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-${arch}.tar.gz" | tar -C /usr/local -xz
ENV PATH=/usr/local/go/bin:/root/go/bin:$PATH GOFLAGS=-buildvcs=false
RUN go install github.com/wailsapp/wails/v2/cmd/wails@v2.17.0
RUN go install github.com/goreleaser/nfpm/v2/cmd/nfpm@v2.48.0
