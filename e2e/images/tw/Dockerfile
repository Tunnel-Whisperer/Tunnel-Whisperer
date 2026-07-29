FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y \
      ca-certificates curl netcat-openbsd openssl openssh-client \
 && rm -rf /var/lib/apt/lists/*
COPY tw /usr/local/bin/tw
COPY echo-server /usr/local/bin/echo-server
ENV TW_CONFIG_DIR=/etc/tw-test
WORKDIR /shared
CMD ["sleep", "infinity"]
