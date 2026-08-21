#!/usr/bin/env bash
# Off-camera gate for part 3: server2 joins via a REAL invite exchange (the
# video shows it compressed). Same FIFO trick as e2e/harness.go's
# runInviteExchange: the issuer's stdin comes from a fifo opened read-write,
# so its SAS [y/N] prompt can be answered after the code is already printed.
# Ends with server2 started and its tunnel verified.
# Logs: e2e/shared/enroll2.log (issuer) + e2e/shared/join2.log (server2).
set -euo pipefail
COMPOSE="docker compose -f e2e/docker-compose.yaml"

$COMPOSE exec -T admin sh -c 'rm -f /tmp/ap2 /shared/enroll2.log /shared/join2.log; mkfifo -m600 /tmp/ap2'
$COMPOSE exec -T -d admin sh -c 'exec 9<>/tmp/ap2; (tw relay invite <&9 >/shared/enroll2.log 2>&1; echo "EXIT $?" >>/shared/enroll2.log)'

code=""
for _ in $(seq 1 30); do
  code=$($COMPOSE exec -T admin sh -c 'sed -n "s/^Invite code: //p" /shared/enroll2.log 2>/dev/null' | tr -d '[:space:]')
  [ -n "$code" ] && break
  sleep 1
done
[ -n "$code" ] || { echo "gate-enroll2: no invite code minted"; exit 1; }

$COMPOSE exec -T -d server2 sh -c "tw join relay.tw.test $code >/shared/join2.log 2>&1"

ok=""
for _ in $(seq 1 60); do
  if $COMPOSE exec -T admin grep -q "read back EXACTLY" /shared/enroll2.log 2>/dev/null; then ok=1; break; fi
  sleep 1
done
[ -n "$ok" ] || { echo "gate-enroll2: issuer never reached the SAS prompt"; exit 1; }
$COMPOSE exec -T admin sh -c 'echo y > /tmp/ap2'

ok=""
for _ in $(seq 1 120); do
  if $COMPOSE exec -T server2 grep -q "Next: tw server start" /shared/join2.log 2>/dev/null; then ok=1; break; fi
  sleep 1
done
[ -n "$ok" ] || { echo "gate-enroll2: join never completed"; exit 1; }

$COMPOSE exec -T -d server2 sh -c 'tw server start >/var/log/tw-server.log 2>&1'
for _ in $(seq 1 60); do
  $COMPOSE exec -T server2 tw server test >/dev/null 2>&1 && exit 0
  sleep 2
done
echo "gate-enroll2: server2 tunnel never came up"
exit 1
