# Maplet: The Natural "Black Metal" Demo
# ---------------------------------------------

Output demo.gif
Set Shell zsh
Set FontSize 15
Set Width 1550
Set Height 950
Set Padding 20
Set TypingSpeed 100ms # A more "human" typing speed

# Custom "Black Metal (bathory)" Inspired Theme
Set Theme { "background": "#0f0f0f", "foreground": "#c1c1c1", "cursor": "#c1c1c1", "black": "#000000", "red": "#875f5f", "green": "#87af87", "yellow": "#afaf87", "blue": "#8787af", "magenta": "#af87af", "cyan": "#5f8787", "white": "#c1c1c1", "brightBlack": "#5f5f5f", "brightRed": "#875f5f", "brightGreen": "#87af87", "brightYellow": "#afaf87", "brightBlue": "#8787af", "brightMagenta": "#af87af", "brightCyan": "#5f8787", "brightWhite": "#eeeeee" }

# 1. Start Tmux first so we can use its splits (looks like a normal dev setup)
Type "cd /home/abee/code/projects/demo/cafe-test && tmux -u"
Enter
Sleep 1s

# 2. Launch Maplet (The "Center Stage")
Type "maplet flow"
Enter
Sleep 4s # Watch Maplet open and show the project structure

# 3. Open a "Tale of Two Terminals" (Split the window)
# We wait after Ctrl+B to ensure tmux is ready for the %
Ctrl+B
Sleep 200ms
Type "%"
Sleep 1s

# 4. Trigger the simulation
# This shows the app running while Maplet observes
Type 'maplet run "python main.py"'
Enter
Sleep 8s # Observe the live heatmap and execution flow

# 5. Bring focus back to Maplet to explore
Ctrl+B
Sleep 200ms
# In case prefix+q is needed to select pane 0
Type "q0" 
Sleep 2s

# 6. Pause and Scrub History
Type " " # Pause
Sleep 1s
Type "H" # Scrub backward
Sleep 1s
Type "H"
Sleep 1s
Type "L" # Scrub forward
Sleep 2s

# 7. Impact/Trace Switch
Type "i"      # Switch to right pane
Sleep 1s
Type "t"      # Cycle modes
Sleep 2s
Type "t"      # Cycle modes
Sleep 3s

# 8. Navigation Jump
Enter # Open editor
Sleep 4s

# 9. Finished
Type ":q"
Enter
Sleep 1s
Ctrl+B
Type ":"
Type "kill-session"
Enter
Sleep 1s
