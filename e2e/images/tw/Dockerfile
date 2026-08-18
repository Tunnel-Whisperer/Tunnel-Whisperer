FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y \
      ca-certificates curl netcat-openbsd openssl openssh-client openssh-server \
 && rm -rf /var/lib/apt/lists/*
COPY tw /usr/local/bin/tw
COPY echo-server /usr/local/bin/echo-server
COPY socks5-server /usr/local/bin/socks5-server
ENV TW_CONFIG_DIR=/etc/tw-test
WORKDIR /shared
CMD ["sleep", "infinity"]
