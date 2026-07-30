# Part 1/6: title card + admin provisions the relay. See record.sh
# for the shared conventions (gates, output filtering, freeze avoidance).

Output e2e/video/out/part-01.mp4

Set Shell "bash"
Set FontSize 30
Set Width 1920
Set Height 1080
Set Padding 24
Set Theme "Catppuccin Mocha"
Set TypingSpeed 40ms
Set Framerate 24

Require docker

Hide
Type@1ms "clear; cat e2e/video/title.txt"
Enter
Show
Sleep 8s
Hide
Type@3ms `bash e2e/video/caption.sh "Step 1 — Admin: provision the relay" "On your admin laptop. We use the manual path (own VPS);" "the wizard path (Hetzner / DigitalOcean / AWS) is one command less."`
Enter
Show
Sleep 4s
Hide
Type@1ms "clear; docker compose -f e2e/docker-compose.yaml exec admin bash"
Enter
Sleep 2s
Type@1ms `export PS1='\[\e[1;36m\]admin@admin\[\e[0m\]:\w\$ ' && cd /shared && clear`
Enter
Sleep 1s
Show
Sleep 1s
Type "tw relay create --provider manual --domain relay.tw.test --ip 172.28.0.10"
Enter
Wait+Screen@60s /not yet installed/
Sleep 4s
Hide
Type@1ms "exit"
Enter
Sleep 1s
Type@3ms `bash e2e/video/caption.sh "Copy the install script to the VPS and run it as root." "Here the 'VPS' is the relay container. Create the DNS A record" "for your domain; TLS comes up automatically."`
Enter
Show
Sleep 4s
Hide
Type@1ms "clear; docker compose -f e2e/docker-compose.yaml exec relay bash"
Enter
Sleep 2s
Type@1ms `export PS1='\[\e[1;31m\]root@relay-vps\[\e[0m\]:\w\$ ' && cd /shared && clear`
Enter
Sleep 1s
Show
Sleep 1s
# Filtered to step markers: full apt/curl output freezes VHS's screen-follow.
Type `bash tw-install-relay.tw.test.sh 2>&1 | grep -E '\[./7\]|==='`
Enter
Sleep 4s
Hide
Wait+Screen@300s /=== Setup complete ===/
Show
Sleep 3s
Hide
Type@1ms "exit"
Enter
Sleep 1s
# Host gates: prep job done (CA trust, sshd, keys), then first green relay test.
Type@1ms "until [ -f e2e/shared/.video-prep-done ]; do sleep 1; done; echo GA''TE-PREP"
Enter
Wait+Screen@180s /GATE-PREP/
Type@5ms "bash e2e/video/gate-relay-warm.sh && echo WA''RM1-OK"
Enter
Wait+Screen@180s /WARM1-OK/
Type@1ms "clear; docker compose -f e2e/docker-compose.yaml exec admin bash"
Enter
Sleep 2s
Type@1ms `export PS1='\[\e[1;36m\]admin@admin\[\e[0m\]:\w\$ ' && cd /shared && clear`
Enter
Sleep 1s
Show
Sleep 1s
Type "tw relay test"
Enter
Wait+Screen@120s /tunnel and shell working/
Sleep 4s
