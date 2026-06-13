property nodeBin : "/opt/homebrew/bin/node"

on run
	set appRoot to POSIX path of ((path to home folder as text) & "Applications:codex-app-proxy")
	set launchCommand to "cd " & quoted form of appRoot & " && clear && " & quoted form of nodeBin & " src/server.js"
	tell application "Terminal"
		activate
		do script launchCommand
	end tell
end run
