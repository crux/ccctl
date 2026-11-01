# The TUI: projects, sessions, resume an earlier session, jump to a
# running one.
Output "/out/ui.gif"

Set Shell "zsh"
Set FontSize 14
Set Width 1260
Set Height 620
Set Padding 14
Set Theme "Catppuccin Mocha"
Set FontFamily "BlexMono Nerd Font Mono"
Set TypingSpeed 55ms
Env HOME "/home/dev"
Env ZDOTDIR "/demo"
Env LANG "C.UTF-8"
Env LC_ALL "C.UTF-8"

Hide
Type "exec bash /demo/start.sh"
Enter
Sleep 2s
Show

Sleep 500ms
Type "ccctl ui"
Sleep 300ms
Enter
Sleep 2.5s

# :projects -> webshop -> its sessions
Down
Sleep 700ms
Enter
Sleep 2s
Down
Sleep 700ms
Down
Sleep 900ms

# resume an earlier session, then come back
Enter
Sleep 2.5s
Enter
Sleep 1.5s

# :active -> jump to a running session
Type "3"
Sleep 2s
Down
Sleep 900ms
Enter
Sleep 3s
