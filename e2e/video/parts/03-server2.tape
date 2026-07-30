# Part 3/6: server2 enrolls (compressed), admin lists both tenants.

Output e2e/video/out/part-03.mp4

Set Shell "bash"
Set FontSize 30
Set Width 1920
Set Height 1080
Set Padding 24
Set Theme "Catppuccin Mocha"
Set TypingSpeed 40ms
Set Framerate 24

Hide
Type@3ms `bash e2e/video/caption.sh "Step 3 — server2: the same dance" "Enrollment is live: server1 keeps running, no relay restart."`
Enter
Show
Sleep 3s
Hide
Type@1ms "clear; docker compose -f e2e/docker-compose.yaml exec server2 bash"
Enter
Sleep 2s
Type@1ms `export PS1='\[\e[1;32m\]root@server2\[\e[0m\]:\w\$ ' && cd /shared && clear`
Enter
Sleep 1s
Show
Type@20ms "tw server join-relay relay.tw.test"
Enter
Wait+Screen@60s /Join request written/
Sleep 2s
Hide
Type@1ms "exit"
Enter
Sleep 1s
# Admin enrolls server2 for real, off camera (gate-enroll2.sh, logged, with
# one self-heal retry). Short typed line: fast-typing a ~300-char line is an
# input burst that freezes VHS's screen-follow, same as output bursts.
Type@5ms "bash e2e/video/gate-enroll2.sh && echo EN2''-OK || echo EN2''-BAD"
Enter
Wait+Screen@180s /EN2-OK/
Type@1ms "clear; docker compose -f e2e/docker-compose.yaml exec server2 bash"
Enter
Sleep 2s
Type@1ms `export PS1='\[\e[1;32m\]root@server2\[\e[0m\]:\w\$ ' && cd /shared && clear`
Enter
Sleep 1s
Show
Type@20ms "tw server join-relay --apply tw_join_response_server2-*.json"
Enter
Wait+Screen@60s /Joined relay/
Type@20ms "nohup tw server start >/var/log/tw-server.log 2>&1 &"
Enter
Sleep 2s
Hide
Type@1ms `for i in $(seq 1 60); do tw server test >/dev/null 2>&1 && break; sleep 2; done; echo GA''TE-S2TEST`
Enter
Wait+Screen@180s /GATE-S2TEST/
Type@1ms "exit"
Enter
Sleep 1s
Type@1ms "clear; docker compose -f e2e/docker-compose.yaml exec admin bash"
Enter
Sleep 2s
Type@1ms `export PS1='\[\e[1;36m\]admin@admin\[\e[0m\]:\w\$ ' && cd /shared && clear`
Enter
Sleep 1s
Show
Sleep 1s
Type "tw relay get-servers"
Enter
Wait+Screen@60s /server2-/
Sleep 5s
