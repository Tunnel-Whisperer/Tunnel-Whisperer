# Part 2/6: enroll server1 (join -> enroll -> apply -> start -> test).

Output e2e/video/out/part-02.mp4

Set Shell "bash"
Set FontSize 30
Set Width 1920
Set Height 1080
Set Padding 24
Set Theme "Catppuccin Mocha"
Set TypingSpeed 40ms
Set Framerate 24

Hide
Type@3ms `bash e2e/video/caption.sh "Step 2 — Enroll server1 (join -> enroll -> apply)" "The server generates a join request; the admin enrolls it on the" "relay; the server applies the response. Files travel over any" "trusted channel."`
Enter
Show
Sleep 5s
Hide
Type@1ms "clear; docker compose -f e2e/docker-compose.yaml exec server bash"
Enter
Sleep 2s
Type@1ms `export PS1='\[\e[1;32m\]root@server1\[\e[0m\]:\w\$ ' && cd /shared && clear`
Enter
Sleep 1s
Show
Sleep 1s
Type "tw server join-relay relay.tw.test"
Enter
Wait+Screen@60s /Join request written/
Sleep 4s
Hide
Type@1ms "exit"
Enter
Sleep 1s
Type@3ms `bash e2e/video/caption.sh "Send the join file to the admin." "(any trusted channel — here it's the demo's shared folder)"`
Enter
Show
Sleep 3s
Hide
Type@1ms "clear; docker compose -f e2e/docker-compose.yaml exec admin bash"
Enter
Sleep 2s
Type@1ms `export PS1='\[\e[1;36m\]admin@admin\[\e[0m\]:\w\$ ' && cd /shared && clear`
Enter
Sleep 1s
Show
Sleep 1s
Type "tw relay enroll-server tw_join_server1-*.json"
Enter
Sleep 5s
Hide
Wait+Screen@120s /Send the response back/
Show
Sleep 4s
Hide
Type@1ms "exit"
Enter
Sleep 1s
Type@3ms `bash e2e/video/caption.sh "Send the response back to server1, then apply and start." "(demo runs the server backgrounded; on a real box:" "sudo tw service install && sudo tw service start)"`
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
Show
Sleep 1s
Type "tw server join-relay --apply tw_join_response_server1-*.json"
Enter
Wait+Screen@60s /Joined relay/
Sleep 3s
Type "nohup tw server start >/var/log/tw-server.log 2>&1 &"
Enter
Sleep 2s
Hide
Type@1ms `for i in $(seq 1 60); do tw server test >/dev/null 2>&1 && break; sleep 2; done; clear; echo GA''TE-S1TEST`
Enter
Wait+Screen@180s /GATE-S1TEST/
Type@1ms "clear"
Enter
Show
Type "tw server test"
Enter
Wait+Screen@120s /tunnel and shell working/
Sleep 4s
