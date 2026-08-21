# Part 4/6: invite the client users. Same two-pane ceremony as part 2, one
# level down: server1 (left) mints client1's invite, the client machine
# (right) redeems it — the client context arrives during the ceremony, so
# there is no bundle file to send. server2/client2 mirror it off camera
# (gate-users2.sh).

Output e2e/video/out/part-04.mp4

Set Shell "bash"
Set FontSize 30
Set Width 1920
Set Height 1080
Set Padding 24
Set Theme "Catppuccin Mocha"
Set TypingSpeed 40ms
Set Framerate 24

Hide
Type@3ms `bash e2e/video/caption.sh "Step 4 — Invite the client users (on the servers)" "Each client gets a user with a port map clientPort:serverPort" "(for SSH the server port is 22). The invite ceremony delivers the" "client's context directly — no bundle files."`
Enter
Show
Sleep 5s
Hide
Type@1ms "clear; tmux -f /dev/null new-session -s vid"
Enter
Sleep 1s
Type@1ms "tmux set -g status off; tmux split-window -h; clear"
Enter
Sleep 1s
# split-window moved focus to the RIGHT pane: make it client1
Type@1ms "clear; docker compose -f e2e/docker-compose.yaml exec client bash"
Enter
Sleep 2s
Type@1ms `export PS1='\[\e[1;33m\]root@client1\[\e[0m\]:\w\$ ' && cd /shared && clear`
Enter
Sleep 1s
Ctrl+B
Type@500ms "o"
Sleep 1s
Type@1ms "clear; docker compose -f e2e/docker-compose.yaml exec server bash"
Enter
Sleep 2s
Type@1ms `export PS1='\[\e[1;32m\]root@server1\[\e[0m\]:\w\$ ' && cd /shared && clear`
Enter
Sleep 1s
# Persist log-level error for this profile: long slog WARN lines trigger the
# VHS screen-follow freeze.
Type@1ms "tw config current-context --log-level error >/dev/null 2>&1; clear"
Enter
Sleep 1s
Show
Sleep 1s
# Server1 (left): mint client1's invite with her port mapping.
Type "tw server user invite client1 -m 2201:22 | tee /shared/uinvite1.log"
Enter
Wait+Screen@60s /Invite code:/
Sleep 3s
Hide
Ctrl+B
Type@500ms "o"
Show
Sleep 1s
# Client (right): redeem it.
Type `tw join relay.tw.test $(sed -n 's/^Invite code: //p' /shared/uinvite1.log)`
Enter
Wait+Screen@60s /read back EXACTLY/
Sleep 4s
Hide
Ctrl+B
Type@500ms "o"
Show
Sleep 1s
# Both panes show the same SAS — approve.
Type "y"
Enter
# Gate on the JOIN side's epilogue: it proves the grant arrived AND that the
# client pane is back at a shell. The issuer's own success line wraps across
# the narrow pane border ("client context delivere / d).") so a regex on it
# can never match the rendered screen.
Wait+Screen@120s /Next: tw client start/
Sleep 3s
Type "clear"
Enter
Sleep 1s
Type "tw server user list"
Enter
Wait+Screen@30s /client1/
Sleep 4s
Hide
Type@1ms "exit"
Enter
Sleep 1s
Ctrl+B
Type@500ms "o"
Type@1ms "exit"
Enter
Sleep 1s
Type@1ms "tmux kill-server"
Enter
Sleep 1s
# server2 mirrors this for client2 — run it for real, off camera.
Type@5ms "bash e2e/video/gate-users2.sh && echo US2''-OK || echo US2''-BAD"
Enter
Wait+Screen@300s /US2-OK/
Type@3ms `bash e2e/video/caption.sh "server2 mirrors this for client2 (port 2202)." "Each client's context arrived during its ceremony —" "nothing to export, nothing to send."`
Enter
Show
Sleep 4s
