# Cover still (2000x840, dev.to cover at 2x): the TUI browse view with
# a running session selected. Only the screenshot is kept.
Output "/tmp/cover.gif"

Set Shell "zsh"
Set FontSize 24
Set Width 2000
Set Height 840
Set Padding 28
Set Theme "Catppuccin Mocha"
Set FontFamily "BlexMono Nerd Font Mono"
Env HOME "/home/dev"
Env ZDOTDIR "/demo"
Env LANG "C.UTF-8"
Env LC_ALL "C.UTF-8"

Hide
Type "exec bash /demo/start.sh"
Enter
Sleep 2s
Type "tmux set -g status off; clear"
Enter
Sleep 300ms
Type "ccctl ui"
Enter
Sleep 2.5s
# :projects -> webshop (first) -> its sessions, the running one stays selected
Enter
Sleep 1.5s
# smaller top row (each - is 5 %), so the detail pane shows every field
Type "---"
Sleep 1s
Show
Sleep 500ms
Screenshot "/out/cover.png"
Sleep 200ms
