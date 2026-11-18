# The Linux test image plus what a desktop has: GTK and WebKitGTK, a virtual screen (Xvfb), a session bus,
# and a keyring. Used to start the app and take screenshots without a real Linux desktop.
FROM localsites-linux
USER root
ARG DEBIAN_FRONTEND=noninteractive
RUN apt-get update && apt-get install -y --no-install-recommends libgtk-3-0t64 libwebkit2gtk-4.1-0 xvfb xauth imagemagick \
      dbus dbus-x11 gnome-keyring libnotify-bin fonts-dejavu-core fonts-noto-color-emoji xdotool \
    && rm -rf /var/lib/apt/lists/*
USER dev
