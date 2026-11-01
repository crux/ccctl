# ps, then focus via tab completion: jump into a running session.
Output "/out/hero.gif"

Set Shell "zsh"
Set FontSize 14
Set Width 1260
Set Height 500
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

Sleep 600ms
Type "ccctl ps"
Sleep 400ms
Enter
Sleep 3.5s

Type "ccctl focus "
Sleep 500ms
Tab
Sleep 1.6s
Tab
Sleep 900ms
Down
Sleep 1.2s
Enter
Sleep 900ms
Enter
Sleep 4s
