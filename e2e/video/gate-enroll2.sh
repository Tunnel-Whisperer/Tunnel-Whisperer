#!/usr/bin/env bash
# Off-camera gate for part 3: admin enrolls server2 for real (the video shows
# it compressed). One self-heal retry (un-enroll first) covers a transient
# step-4 SSH-dial loss against the local_certs shim race. Log: e2e/shared/enroll2.log.
docker compose -f e2e/docker-compose.yaml exec -T admin sh -c '
  cd /shared &&
  f=$(ls tw_join_server2-*.json) &&
  id=$(basename "$f" .json) && id=${id#tw_join_} &&
  { tw relay enroll-server "$f" ||
    { tw relay un-enroll-server "$id" --yes; sleep 3; tw relay enroll-server "$f"; }; }
' </dev/null > e2e/shared/enroll2.log 2>&1
