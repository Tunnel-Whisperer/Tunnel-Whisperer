#!/usr/bin/env bash
# Off-camera gate for part 1: wait until the admin's `tw relay test` first
# passes (cert issuance settling after install). Exit code is the gate.
for _ in $(seq 1 60); do
  docker compose -f e2e/docker-compose.yaml exec -T admin tw relay test >/dev/null 2>&1 && exit 0
  sleep 2
done
exit 1
