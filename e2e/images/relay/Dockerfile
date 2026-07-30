# Relay base: Debian + systemd with the install script's apt packages pre-baked
# for speed. The REAL tw-generated install script provisions this container at
# test time; keep the apt package list in sync with
# internal/relay/terraform/install-script.sh.tmpl.
#
# Xray is deliberately NOT pre-baked. The install script always re-downloads and
# re-installs the pinned Xray version anyway (so pre-baking saves nothing), and
# pre-baking actively breaks it: the upstream Xray installer's final step is
# `[[ "$XRAY_RUNNING" -eq 1 ]] && start_xray`, whose exit status becomes the
# installer's. When Xray is already installed (XRAY_IS_INSTALLED_BEFORE=1) but
# not currently running, that test is false and the installer exits 1 under the
# script's `set -e` — even though the install succeeded. A fresh VM never hits
# this (Xray absent → the installer's clean `systemctl start/enable` branch, exit
# 0), so leaving Xray out of the image makes the test path match a fresh VM
# exactly. Product code is untouched.
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
 && rm -rf /var/lib/apt/lists/*
STOPSIGNAL SIGRTMIN+3
CMD ["/sbin/init"]
