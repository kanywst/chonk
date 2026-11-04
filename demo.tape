# Recorded on canned data (CHONK_DEMO), so the demo never shows a real machine.
Output demo.gif

Require go

Set Shell "bash"
Set FontSize 16
Set Width 1280
Set Height 760
Set Padding 20
Set TypingSpeed 60ms
Set Theme "Catppuccin Mocha"
Set WindowBar Colorful
Set WindowBarSize 36
Set BorderRadius 10

Hide
Type `go build -o /tmp/chonk ./cmd/chonk && export CHONK_DEMO=1 PATH="/tmp:$PATH" && clear && echo "TAPE""_READY"`
Enter
Wait+Screen@300s /TAPE_READY/
Ctrl+L
Show

Type "chonk"
Sleep 400ms
Enter
Sleep 3.5s

# The VM: 81G on disk, 3.6G in use inside. Get the trapped space back.
Enter
Sleep 3.5s
Type "1"
Sleep 2.5s
Type "y"
Sleep 4s

# Back to the list: free space went up.
Escape
Sleep 2.5s

# A Rust project's build output, cleaned with cargo's own command.
Type "g"
Sleep 400ms
Enter
Sleep 3s
Escape
Sleep 800ms

# Filter.
Type "/"
Sleep 300ms
Type "cache"
Sleep 1.5s
Enter
Sleep 1.5s
Type "q"
Sleep 500ms
