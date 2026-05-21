package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

func confirmMissingYes(config commandConfig, prompt string, missingYesMessage string) (bool, error) {
	if !isTerminalInput(config) {
		return false, fmt.Errorf("%s", missingYesMessage)
	}
	if config.stderr != nil {
		fmt.Fprintf(config.stderr, "%s [y/N] ", prompt)
	}
	answer, err := bufio.NewReader(config.stdin).ReadString('\n')
	if err != nil && err != io.EOF {
		return false, err
	}
	answer = strings.ToLower(strings.TrimSpace(answer))
	if answer == "y" || answer == "yes" {
		return true, nil
	}
	return false, fmt.Errorf("delete canceled")
}

func isTerminalInput(config commandConfig) bool {
	if config.stdinIsTerminal != nil {
		return config.stdinIsTerminal(config.stdin)
	}
	file, ok := config.stdin.(*os.File)
	return ok && term.IsTerminal(int(file.Fd()))
}
