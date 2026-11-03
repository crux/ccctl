# status shows the size; find searches all of it.
Output "/out/find.gif"

Set Shell "zsh"
Set FontSize 16
Set Width 1440
Set Height 560
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
Type "ccctl find warmup >/dev/null; clear"
Enter
Sleep 2s
Show

Sleep 600ms
Type "ccctl status"
Sleep 400ms
Enter
Sleep 3.5s

Hide
Type "clear"
Enter
Sleep 300ms
Show

Type 'time ccctl find "rate limit"'
Sleep 400ms
Enter
Sleep 5s
