#!/usr/bin/env bash
# Off-camera gate for part 4: server2 mirrors the client-user trio for client2
# (the video shows it as a caption). Log: e2e/shared/users2.log.
docker compose -f e2e/docker-compose.yaml exec -T server2 sh -c '
  cd /shared &&
  tw server user create client2 -m 2202:22 &&
  tw server user apply client2 &&
  tw config export-user client2
' </dev/null > e2e/shared/users2.log 2>&1
