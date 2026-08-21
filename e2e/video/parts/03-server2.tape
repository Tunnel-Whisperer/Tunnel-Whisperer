# Part 3/6: server2 enrolls (same invite ceremony, compressed — run for real
# off camera by gate-enroll2.sh), then the admin lists both tenants.

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
Type@3ms `bash e2e/video/caption.sh "Step 3 — server2: the same dance" "Same spoken-invite ceremony (run for real, off camera)." "Enrollment is live: server1 keeps running, no relay restart."`
Enter
Show
Sleep 4s
Hide
# The whole invite/join/start for server2 runs for real via the gate script.
# Short typed line: fast-typing a long line is an input burst that freezes
# VHS's screen-follow, same as output bursts.
Type@5ms "bash e2e/video/gate-enroll2.sh && echo EN2''-OK || echo EN2''-BAD"
Enter
Wait+Screen@300s /EN2-OK/
Type@1ms "clear; docker compose -f e2e/docker-compose.yaml exec server2 bash"
Enter
Sleep 2s
Type@1ms `export PS1='\[\e[1;32m\]root@server2\[\e[0m\]:\w\$ ' && cd /shared && clear`
Enter
Sleep 1s
Show
Type "tw server test"
Enter
Wait+Screen@120s /tunnel and shell working/
Sleep 3s
Hide
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
Hide
Type@1ms "exit"
Enter
Sleep 1s
