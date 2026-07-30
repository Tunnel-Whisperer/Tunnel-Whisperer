# Part 6/6: final verify from the admin + end card.

Output e2e/video/out/part-06.mp4

Set Shell "bash"
Set FontSize 30
Set Width 1920
Set Height 1080
Set Padding 24
Set Theme "Catppuccin Mocha"
Set TypingSpeed 40ms
Set Framerate 24

Hide
Type@1ms "clear; docker compose -f e2e/docker-compose.yaml exec admin bash"
Enter
Sleep 2s
Type@1ms `export PS1='\[\e[1;36m\]admin@admin\[\e[0m\]:\w\$ ' && cd /shared && clear`
Enter
Sleep 1s
Show
Sleep 1s
Type "tw relay test && tw relay get-servers"
Enter
Wait+Screen@120s /server2-/
Sleep 5s
Hide
Type@1ms "exit"
Enter
Sleep 1s
Type@3ms `bash e2e/video/caption.sh "Done: 1 relay, 2 servers, 2 clients." "Full guide: Guides -> Multi-Server Walkthrough in the docs." "https://tunnel-whisperer.github.io/Tunnel-Whisperer/"`
Enter
Show
Sleep 6s
