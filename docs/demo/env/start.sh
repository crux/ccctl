#!/usr/bin/env bash
# Run by the tapes (hidden, with exec): starts tmux with a shell window
# and the running sessions, writes their session records, and attaches.
export HOME=/home/dev ZDOTDIR=/demo LANG=C.UTF-8 LC_ALL=C.UTF-8
. /demo/sessions.sh
tmux kill-server 2>/dev/null
rm -f "$HOME"/.claude/sessions/*.json
cd "$HOME" || exit 1
tmux -u -f /demo/tmux.conf new-session -d -s demo -n shell -x 140 -y 34

for entry in "${RUNNING[@]}"; do
	IFS='|' read -r win id cwd title st <<< "$entry"
	tmux new-window -d -t demo -n "$win" -c "$cwd" -e "CLAUDE_DEMO_TITLE=$title" 'exec claude'
done
sleep 0.5

# Session records, as Claude Code writes them: ~/.claude/sessions/<pid>.json.
for entry in "${RUNNING[@]}"; do
	IFS='|' read -r win id cwd title st <<< "$entry"
	pid=$(tmux list-panes -t "demo:$win" -F '#{pane_pid}')
	printf '{"pid":%s,"sessionId":"%s","cwd":"%s","version":"2.1.316","name":"%s-1","nameSource":"derived","status":"%s"}\n' \
		"$pid" "$id" "$cwd" "$win" "$st" > "$HOME/.claude/sessions/$pid.json"
done

tmux select-window -t demo:shell
exec tmux -u attach -t demo
