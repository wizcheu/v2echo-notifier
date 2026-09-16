# Selkies provides a remotely operated Chromium window, without a NAS desktop.
FROM lscr.io/linuxserver/chromium:latest
USER root
RUN apt-get update && apt-get install -y --no-install-recommends python3 python3-websocket \
    && rm -rf /var/lib/apt/lists/*
COPY bridge.py /opt/v2echo-browser/bridge.py
COPY autostart /defaults/autostart
COPY autostart /defaults/autostart_wayland
RUN chmod 0755 /defaults/autostart /defaults/autostart_wayland
ENV TITLE="V2Echo 浏览器验证" SUBFOLDER="/browser-desktop/"
# 8090 and the inherited 3000 are private to the Compose network.
EXPOSE 8090
