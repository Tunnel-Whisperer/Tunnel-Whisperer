# Part 5/6: client1 imports its bundle, connects, and SSHes to server1
# through the tunnel.

Output e2e/video/out/part-05.mp4

Set Shell "bash"
Set FontSize 30
Set Width 1920
Set Height 1080
Set Padding 24
Set Theme "Catppuccin Mocha"
Set TypingSpeed 40ms
Set Framerate 24

Hide
Type@3ms `bash e2e/video/caption.sh "Step 5 — On client1: import the bundle and connect"`
Enter
Show
Sleep 3s
Hide
Type@1ms "clear; docker compose -f e2e/docker-compose.yaml exec client bash"
Enter
Sleep 2s
Type@1ms `export PS1='\[\e[1;33m\]root@client1\[\e[0m\]:\w\$ ' && cd /shared && clear`
Enter
Sleep 1s
Type@1ms "tw config current-context --log-level error >/dev/null 2>&1; clear"
Enter
Sleep 1s
Show
Sleep 1s
Type "tw config import client1-tw-context.twctx --activate"
Enter
Wait+Screen@30s /Imported and switched to context/
Sleep 3s
Type "nohup tw client connect >/var/log/tw-client.log 2>&1 &"
Enter
Sleep 2s
Hide
Type@1ms `for i in $(seq 1 60); do nc -z 127.0.0.1 2201 && break; sleep 1; done; clear; echo GA''TE-NC`
Enter
Wait+Screen@120s /GATE-NC/
Type@1ms "clear"
Enter
Show
Type "ssh -p 2201 root@127.0.0.1"
Enter
Wait+Screen@30s /yes\/no/
Sleep 1s
Type "yes"
Enter
Wait+Screen@30s /root@server1/
Sleep 2s
Type "hostname"
Enter
Sleep 3s
Type "exit"
Enter
Sleep 1s
Hide
Type@1ms "exit"
Enter
Sleep 1s
Type@3ms `bash e2e/video/caption.sh "That SSH session crossed: client1 -> relay (HTTPS/mTLS) -> server1." "tw provides the tunnel to port 22 — SSH authentication is still" "the server's own accounts. client2 does the same with port 2202."`
Enter
Show
Sleep 5s
