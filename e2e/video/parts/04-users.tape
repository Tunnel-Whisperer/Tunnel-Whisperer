# Part 4/6: create the client users on the servers, export bundles.

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
Type@3ms `bash e2e/video/caption.sh "Step 4 — Create the client users (on the servers)" "Each client gets a user with a port map clientPort:serverPort." "For SSH the server port is 22."`
Enter
Show
Sleep 4s
Hide
Type@1ms "clear; docker compose -f e2e/docker-compose.yaml exec server bash"
Enter
Sleep 2s
Type@1ms `export PS1='\[\e[1;32m\]root@server1\[\e[0m\]:\w\$ ' && cd /shared && clear`
Enter
Sleep 1s
# Persist log-level error for this profile: the user-create/apply slog WARN
# lines are long enough to trigger the VHS screen-follow freeze.
Type@1ms "tw config current-context --log-level error >/dev/null 2>&1; clear"
Enter
Sleep 1s
Show
Sleep 1s
Type "tw server user create client1 -m 2201:22"
Enter
Wait+Screen@30s /created with 1 port mapping/
Sleep 2s
Type "tw server user apply client1"
Enter
Wait+Screen@60s /Done\./
Sleep 2s
# Keep the screen from ever scrolling: scrolling reliably freezes the VHS
# screen-follow (parts 1-3 never scroll and never froze; part 4 froze at its
# scroll point three runs straight).
Type "clear"
Enter
Sleep 1s
Type "tw config export-user client1"
Enter
Wait+Screen@30s /Exported/
Sleep 4s
Hide
Type@1ms "exit"
Enter
Sleep 1s
# server2 mirrors this for client2 — run it for real, off camera
# (gate-users2.sh, logged). Short typed line: see part 3's note on input bursts.
Type@5ms "bash e2e/video/gate-users2.sh && echo US2''-OK || echo US2''-BAD"
Enter
Wait+Screen@120s /US2-OK/
Type@3ms `bash e2e/video/caption.sh "server2 mirrors this for client2 (port 2202)." "Send each .twctx bundle to its client over a trusted channel —" "bundles are unprotected; treat them like a private key."`
Enter
Show
Sleep 4s
