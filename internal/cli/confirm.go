package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"golang.org/x/term"
)

func confirmMissingYes(config commandConfig, prompt string, missingYesMessage string) (bool, error) {
	return confirmMissingYesNamed(config, prompt, missingYesMessage, "delete canceled")
}

// confirmMissingYesNamed is confirmMissingYes with the wording of the decline
// error under the caller's control, so a prompt that is not about deletion does
// not report one.
func confirmMissingYesNamed(config commandConfig, prompt string, missingYesMessage string, canceledMessage string) (bool, error) {
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
	return false, fmt.Errorf("%s", canceledMessage)
}

// promptChoice asks the user to pick one of the numbered options and returns
// its index. Anything that is not one of the offered numbers cancels.
func promptChoice(config commandConfig, prompt string, options []string) (int, error) {
	if !isTerminalInput(config) {
		return 0, fmt.Errorf("cannot ask which one was meant without a terminal")
	}
	if config.stderr != nil {
		fmt.Fprintf(config.stderr, "%s\n", prompt)
		for index, option := range options {
			fmt.Fprintf(config.stderr, "  %d) %s\n", index+1, option)
		}
		fmt.Fprintf(config.stderr, "Which one? [1-%d] ", len(options))
	}
	answer, err := bufio.NewReader(config.stdin).ReadString('\n')
	if err != nil && err != io.EOF {
		return 0, err
	}
	answer = strings.TrimSpace(answer)
	choice, err := strconv.Atoi(answer)
	if err != nil || choice < 1 || choice > len(options) {
		return 0, fmt.Errorf("canceled: %q is not one of 1-%d", answer, len(options))
	}
	return choice - 1, nil
}

func isTerminalInput(config commandConfig) bool {
	if config.stdinIsTerminal != nil {
		return config.stdinIsTerminal(config.stdin)
	}
	file, ok := config.stdin.(*os.File)
	return ok && term.IsTerminal(int(file.Fd()))
}
