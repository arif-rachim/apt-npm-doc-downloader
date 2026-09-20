package main

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

var stdin = bufio.NewReader(os.Stdin)

// interactive reports whether we may ask the user questions: only when stdin
// is a terminal and -yes was not given, so scripted runs never block.
// "plan" still asks, because resolving is exactly what it is there for; only
// the offer to save a repository is held back until something was downloaded.
func interactive(o *options) bool {
	if o.assumeYes {
		return false
	}
	info, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// interactiveStdin reports whether stdin is a terminal, for commands that do
// not carry an options struct.
func interactiveStdin() bool {
	info, err := os.Stdin.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// confirm asks a yes/no question. def is the answer used when the user just
// presses Enter.
func confirm(question string, def bool) bool {
	hint := "[y/N]"
	if def {
		hint = "[Y/n]"
	}
	fmt.Printf("%s %s: ", question, hint)
	line, err := stdin.ReadString('\n')
	if err != nil {
		return def
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "":
		return def
	case "y", "ya", "yes":
		return true
	default:
		return false
	}
}

// askLine reads a free-form answer, returning "" when the user just presses
// Enter.
func askLine(question string) string {
	fmt.Printf("%s: ", question)
	line, err := stdin.ReadString('\n')
	if err != nil {
		return ""
	}
	return strings.TrimSpace(line)
}

// askChoice offers a numbered list and returns the chosen entry, a free-form
// answer, or "" when the user skips. It exists so a wrong name is never a
// dead end: whatever the tool already knows is put on screen to be picked.
func askChoice(prompt string, options []string, limit int) string {
	shown := options
	if limit > 0 && len(shown) > limit {
		shown = shown[:limit]
	}
	for i, o := range shown {
		fmt.Printf("    %2d) %s\n", i+1, o)
	}
	if len(options) > len(shown) {
		fmt.Printf("    ... and %d more\n", len(options)-len(shown))
	}
	fmt.Println("     or type another name, or press Enter to skip")
	line := askLine(prompt)
	if line == "" {
		return ""
	}
	if n, err := strconv.Atoi(line); err == nil && n >= 1 && n <= len(shown) {
		return shown[n-1]
	}
	return line
}
