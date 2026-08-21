# Part 2/6: enroll server1 over a spoken invite (tw relay invite / tw join).
# Two live terminals side by side via tmux panes inside the one vhs session:
# admin (left) mints the invite and approves the SAS, server1 (right) redeems
# it. Every line in both panes is real output — the matching SAS strings on
# screen at the same time ARE the ceremony.

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
Type@3ms `bash e2e/video/caption.sh "Step 2 — Enroll server1 (spoken invite)" "The admin mints a one-time code and reads it to the server operator" "over any channel. Both terminals then show the SAME short string —" "the admin approves only on an exact match. No files change hands."`
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
# split-window moved focus to the RIGHT pane: make it server1
Type@1ms "clear; docker compose -f e2e/docker-compose.yaml exec server bash"
Enter
Sleep 2s
Type@1ms `export PS1='\[\e[1;32m\]root@server1\[\e[0m\]:\w\$ ' && cd /shared && clear`
Enter
Sleep 1s
Ctrl+B
Type@500ms "o"
Sleep 1s
Type@1ms "clear; docker compose -f e2e/docker-compose.yaml exec admin bash"
Enter
Sleep 2s
Type@1ms `export PS1='\[\e[1;36m\]admin@admin\[\e[0m\]:\w\$ ' && cd /shared && clear`
Enter
Sleep 1s
Show
Sleep 1s
# Admin (left): mint the invite. tee captures the code so the server pane can
# "hear" it — the demo's stand-in for reading it aloud.
Type "tw relay invite | tee /shared/invite1.log"
Enter
Wait+Screen@60s /Invite code:/
Sleep 3s
Hide
Ctrl+B
Type@500ms "o"
Show
Sleep 1s
# Server1 (right): redeem the code the admin just read out.
Type `tw join relay.tw.test $(sed -n 's/^Invite code: //p' /shared/invite1.log)`
Enter
Wait+Screen@60s /read back EXACTLY/
Sleep 4s
Hide
Ctrl+B
Type@500ms "o"
Show
Sleep 1s
# Both panes now show the same SAS — the admin confirms the exact match.
Type "y"
Enter
Wait+Screen@120s /enrolled/
# The join side prints its epilogue a beat later — wait for it so the next
# keystrokes land in the server pane's SHELL, not in a still-running tw join.
Wait+Screen@60s /Next: tw server start/
Sleep 3s
Hide
Ctrl+B
Type@500ms "o"
Show
Sleep 1s
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
