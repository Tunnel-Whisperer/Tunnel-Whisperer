#!/usr/bin/env bash
# Off-camera gate for part 4: server2 invites client2 for real and the client
# machine redeems it (the video shows it as a caption). Same FIFO trick as
# gate-enroll2.sh. The client already runs client1's live profile, so the
# client2 context is stored without activating (tw join never touches the
# active context). Logs: e2e/shared/users2.log (issuer) + ujoin2.log (client).
set -euo pipefail
COMPOSE="docker compose -f e2e/docker-compose.yaml"

$COMPOSE exec -T server2 sh -c 'rm -f /tmp/ap4 /shared/users2.log; mkfifo -m600 /tmp/ap4'
$COMPOSE exec -T client sh -c 'rm -f /shared/ujoin2.log'
$COMPOSE exec -T -d server2 sh -c 'exec 9<>/tmp/ap4; (tw server user invite client2 -m 2202:22 <&9 >/shared/users2.log 2>&1; echo "EXIT $?" >>/shared/users2.log)'

code=""
for _ in $(seq 1 30); do
  code=$($COMPOSE exec -T server2 sh -c 'sed -n "s/^Invite code: //p" /shared/users2.log 2>/dev/null' | tr -d '[:space:]')
  [ -n "$code" ] && break
  sleep 1
done
[ -n "$code" ] || { echo "gate-users2: no invite code minted"; exit 1; }

$COMPOSE exec -T -d client sh -c "tw join relay.tw.test $code >/shared/ujoin2.log 2>&1"

ok=""
for _ in $(seq 1 60); do
  if $COMPOSE exec -T server2 grep -q "read back EXACTLY" /shared/users2.log 2>/dev/null; then ok=1; break; fi
  sleep 1
done
[ -n "$ok" ] || { echo "gate-users2: issuer never reached the SAS prompt"; exit 1; }
$COMPOSE exec -T server2 sh -c 'echo y > /tmp/ap4'

for _ in $(seq 1 120); do
  $COMPOSE exec -T server2 grep -q "client context delivered" /shared/users2.log 2>/dev/null && exit 0
  sleep 1
done
echo "gate-users2: client2 enrollment never completed"
exit 1
