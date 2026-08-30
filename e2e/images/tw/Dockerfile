FROM debian:bookworm-slim
# netperf is in Debian non-free; wireguard-tools' Recommends would pull a
# kernel + initramfs into the image, so it is installed without them.
RUN sed -i 's/^Components: main$/Components: main non-free/' /etc/apt/sources.list.d/debian.sources \
 && apt-get update && apt-get install -y \
      ca-certificates curl netcat-openbsd openssl openssh-client openssh-server \
      iproute2 iperf3 netperf ethtool \
 && apt-get install -y --no-install-recommends wireguard-tools \
 && rm -rf /var/lib/apt/lists/*
COPY tw /usr/local/bin/tw
COPY echo-server /usr/local/bin/echo-server
COPY socks5-server /usr/local/bin/socks5-server
ENV TW_CONFIG_DIR=/etc/tw-test
WORKDIR /shared
CMD ["sleep", "infinity"]
