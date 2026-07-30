#!/usr/bin/env bash
# Records the multi-server-walkthrough tutorial video against the live e2e
# Docker topology. Every terminal line in the video is real output from the
# real tw binary running in the containers; long waits are jump-cut by the
# tape, never faked.
#
#   bash e2e/video/record.sh            # render the docs/assets/multi-server-*.gif embeds
#   VIDEO_KEEP=1 bash e2e/video/record.sh   # leave the stack up afterwards
#   VIDEO_PREP_ONLY=1 bash e2e/video/record.sh  # stack + prep, no vhs (for debugging)
#
# Requires: docker, go, vhs (+ttyd, ffmpeg). See the tapes: parts/*.tape.
set -euo pipefail

cd "$(dirname "$0")/../.."   # repo root
COMPOSE="docker compose -f e2e/docker-compose.yaml"
export PATH="$HOME/go/bin:$HOME/.local/bin:$PATH"

xin() { $COMPOSE exec -T "$1" sh -c "$2"; }

log() { printf '>>> %s\n' "$*"; }

cleanup() {
  local status=$?
  [ -n "${WATCHER_PID:-}" ] && kill "$WATCHER_PID" 2>/dev/null || true
  [ -n "${PREP_PID:-}" ] && kill "$PREP_PID" 2>/dev/null || true
  if [ -z "${VIDEO_KEEP:-}" ]; then
    log "tearing down the stack (VIDEO_KEEP=1 to keep it)"
    make e2e-down >/dev/null 2>&1 || true
  fi
  exit "$status"
}
trap cleanup EXIT

command -v vhs >/dev/null || { echo "vhs not on PATH (go install github.com/charmbracelet/vhs@latest)"; exit 1; }
command -v ttyd >/dev/null || { echo "ttyd not on PATH"; exit 1; }
command -v ffmpeg >/dev/null || { echo "ffmpeg not on PATH"; exit 1; }

log "building bins + starting the compose topology"
make e2e-up

# --- clean state (mirrors the harness's per-scenario wipes) -----------------
kill_matching() { # service, cmdline substring — the tw image has no pkill
  xin "$1" 'for p in /proc/[0-9]*; do
    pid=${p#/proc/}; [ "$pid" = "$$" ] && continue
    cmd=$(tr "\0" " " < "$p/cmdline" 2>/dev/null) || continue
    case "$cmd" in *"'"$2"'"*) kill -9 "$pid" 2>/dev/null ;; esac
  done; true'
}

log "wiping tw state and leftover processes in the tw containers"
for svc in admin server server2 client; do
  kill_matching "$svc" "tw server start"
  kill_matching "$svc" "tw client connect"
  kill_matching "$svc" "echo-server"
  xin "$svc" 'rm -rf /etc/tw-test'
done
rm -f e2e/shared/tw_join*.json e2e/shared/*.twctx e2e/shared/tw-install-*.sh \
      e2e/shared/.video-* e2e/shared/enroll*.log e2e/shared/users2.log e2e/shared/caddy-root.crt

log "waiting for relay systemd boot"
deadline=$(( $(date +%s) + 120 ))
while :; do
  state=$(xin relay 'systemctl is-system-running 2>/dev/null || true' | tr -d '[:space:]')
  case "$state" in running|degraded) break ;; esac
  [ "$(date +%s)" -gt "$deadline" ] && { echo "relay systemd never booted (state: $state)"; exit 1; }
  sleep 2
done

# Fresh-VM shim (same as e2e installShim): the image pre-bakes caddy apt bits
# that make the install script's gpg --dearmor prompt and abort.
xin relay 'rm -f /usr/share/keyrings/caddy-stable-archive-keyring.gpg /etc/apt/sources.list.d/caddy-stable.list'

# --- background helpers ------------------------------------------------------
# local_certs shim watcher: the offline test network has no ACME, so Caddy must
# use its internal CA. Install AND enroll/un-enroll re-render the Caddyfile from
# scratch, wiping the shim. CRUCIAL: only act once the Caddyfile has been stable
# for >=3s — enroll writes the file and then reloads caddy over an SSH session
# that itself runs THROUGH caddy, and restarting caddy inside that window kills
# the admin's tunnel mid-command (observed: `sudo systemctl reload caddy` dying
# with "exited without exit status"). After the settle window, enroll's own
# reload is done and its step-4 fresh-dial retries (~15s budget, see
# internal/ops/user.go) absorb the restart. Same discipline as
# e2e/server_test.go's wait-for-"Caddyfile reloaded"-then-shim dance.
(
  while :; do
    xin relay 'set -- /etc/caddy/Caddyfile
      test -f "$1" || exit 0
      grep -q local_certs "$1" && exit 0
      age=$(( $(date +%s) - $(stat -c %Y "$1") ))
      [ "$age" -ge 3 ] || exit 0
      printf "{\n\tlocal_certs\n}\n" | cat - "$1" > /tmp/Caddyfile.new &&
        mv /tmp/Caddyfile.new "$1" && systemctl restart caddy' 2>/dev/null || true
    sleep 1
  done
) & WATCHER_PID=$!

# One-shot prep job: runs while scene 1's install is on camera. Trust the caddy
# local root everywhere, start sshd on the servers, stage the client SSH key,
# then drop the marker file the tape's hidden gate waits on.
(
  set -e
  while ! xin relay 'grep -q "BEGIN CERTIFICATE" /var/lib/caddy/.local/share/caddy/pki/authorities/local/root.crt 2>/dev/null'; do
    sleep 2
  done
  xin relay 'cp /var/lib/caddy/.local/share/caddy/pki/authorities/local/root.crt /shared/caddy-root.crt'
  for svc in admin server server2 client; do
    xin "$svc" 'cp /shared/caddy-root.crt /usr/local/share/ca-certificates/tw-e2e-root.crt && update-ca-certificates >/dev/null'
  done
  for svc in server server2; do
    xin "$svc" 'mkdir -p /run/sshd && (pgrep -x sshd >/dev/null 2>&1 || /usr/sbin/sshd)'
  done
  xin client 'test -f /root/.ssh/id_ed25519 || ssh-keygen -q -t ed25519 -N "" -f /root/.ssh/id_ed25519'
  xin client 'cat /root/.ssh/id_ed25519.pub' > /tmp/tw-video-client.pub
  for svc in server server2; do
    xin "$svc" "mkdir -p /root/.ssh && chmod 700 /root/.ssh && printf '%s\n' '$(cat /tmp/tw-video-client.pub)' > /root/.ssh/authorized_keys && chmod 600 /root/.ssh/authorized_keys"
  done
  touch e2e/shared/.video-prep-done
  echo ">>> prep job done"
) & PREP_PID=$!

if [ -n "${VIDEO_PREP_ONLY:-}" ]; then
  log "VIDEO_PREP_ONLY set — stack is up, helpers running. Ctrl-C to stop."
  wait "$PREP_PID" || true
  log "prep finished; watcher still running. Sleeping until interrupted."
  wait "$WATCHER_PID" || true
  exit 0
fi

# One tape per scene, each a fresh short VHS session: a single long session's
# screen-follow reliably freezes some minutes in (keystrokes still flow, the
# virtual screen stops updating, Waits go blind). Topology state lives in the
# containers, so scene boundaries are natural cut points; the parts share
# identical Set headers and are losslessly concatenated.
log "rendering per-scene tapes (drives the whole walkthrough live — several minutes)"
mkdir -p docs/assets e2e/video/out
rm -f e2e/video/out/part-*.mp4
for tape in e2e/video/parts/*.tape; do
  log "vhs $tape"
  vhs "$tape"
done

log "concatenating parts"
for f in e2e/video/out/part-*.mp4; do printf "file '%s'\n" "$PWD/$f"; done > e2e/video/out/concat.txt
ffmpeg -y -loglevel error -f concat -safe 0 -i e2e/video/out/concat.txt -c copy e2e/video/out/multi-server-walkthrough.mp4

# The committed embeds are GIFs (GitHub strips <video> tags from rendered
# markdown); the mp4s stay in out/. Two-pass palette per clip, no dithering,
# so the terminal text stays crisp at 960px.
gif() {
  ffmpeg -y -loglevel error -i "$1" -vf "fps=8,scale=960:-1:flags=lanczos,palettegen=stats_mode=diff" e2e/video/out/palette.png
  ffmpeg -y -loglevel error -i "$1" -i e2e/video/out/palette.png \
    -lavfi "fps=8,scale=960:-1:flags=lanczos[x];[x][1:v]paletteuse=dither=none" "$2"
}
log "rendering gifs into docs/assets"
gif e2e/video/out/part-01.mp4 docs/assets/multi-server-step1-relay.gif
gif e2e/video/out/part-02.mp4 docs/assets/multi-server-step2-server1.gif
gif e2e/video/out/part-03.mp4 docs/assets/multi-server-step3-server2.gif
gif e2e/video/out/part-04.mp4 docs/assets/multi-server-step4-users.gif
gif e2e/video/out/part-05.mp4 docs/assets/multi-server-step5-client.gif
gif e2e/video/out/part-06.mp4 docs/assets/multi-server-step6-verify.gif
gif e2e/video/out/multi-server-walkthrough.mp4 docs/assets/multi-server-walkthrough.gif

log "render complete:"
ffprobe -v error -show_entries format=duration,size -of default=noprint_wrappers=1 e2e/video/out/multi-server-walkthrough.mp4
du -h docs/assets/multi-server-*.gif
