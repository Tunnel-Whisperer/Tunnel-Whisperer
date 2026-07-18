# Relay base: Debian + systemd with the install script's packages pre-baked.
# The REAL tw-generated install script provisions this container at test time;
# this image only pre-installs packages so that run is fast. Keep the package
# list in sync with internal/relay/terraform/install-script.sh.tmpl.
FROM debian:bookworm
ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update && apt-get install -y \
      systemd systemd-sysv dbus \
      openssh-server sudo gnupg \
      debian-keyring debian-archive-keyring apt-transport-https curl ufw unzip \
 && curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' \
      | gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg \
 && curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt' \
      > /etc/apt/sources.list.d/caddy-stable.list \
 && apt-get update && apt-get install -y caddy \
 && bash -c "$(curl -L https://github.com/XTLS/Xray-install/raw/main/install-release.sh)" @ install \
 && rm -rf /var/lib/apt/lists/*
STOPSIGNAL SIGRTMIN+3
CMD ["/sbin/init"]
