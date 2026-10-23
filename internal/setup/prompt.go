package setup

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// ErrNoInput is returned when a question is asked but stdin is closed.
var ErrNoInput = errors.New("no input available; run non-interactively with --template ... --yes")

type Choice struct {
	Key   string
	Label string
}

// Prompter asks questions on a line-oriented terminal.
type Prompter struct {
	in  *bufio.Reader
	out io.Writer
}

func NewPrompter(in io.Reader, out io.Writer) *Prompter {
	return &Prompter{in: bufio.NewReader(in), out: out}
}

func (p *Prompter) readLine() (string, error) {
	fmt.Fprint(p.out, "> ")
	line, err := p.in.ReadString('\n')
	if err != nil && (line == "" || !errors.Is(err, io.EOF)) {
		if errors.Is(err, io.EOF) {
			return "", ErrNoInput
		}
		return "", err
	}
	return strings.TrimSpace(line), nil
}

func (p *Prompter) list(q string, choices []Choice, hint string) {
	fmt.Fprintf(p.out, "\n? %s %s\n", q, hint)
	for i, c := range choices {
		fmt.Fprintf(p.out, "  %d) %s\n", i+1, c.Label)
	}
}

// parseNumbers parses "1,3" or "1 3" into zero-based indexes.
func parseNumbers(s string, n int) ([]int, error) {
	var out []int
	seen := map[int]bool{}
	for _, f := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' }) {
		i, err := strconv.Atoi(f)
		if err != nil || i < 1 || i > n {
			return nil, fmt.Errorf("%q is not a number between 1 and %d", f, n)
		}
		if !seen[i] {
			seen[i] = true
			out = append(out, i-1)
		}
	}
	return out, nil
}

// Multi asks for zero or more choices and returns their keys. "all" picks
// every choice.
func (p *Prompter) Multi(q string, choices []Choice) ([]string, error) {
	p.list(q, choices, `(numbers separated by commas, "all", or Enter for none)`)
	for {
		line, err := p.readLine()
		if err != nil {
			return nil, err
		}
		if strings.EqualFold(line, "all") {
			keys := make([]string, len(choices))
			for i, c := range choices {
				keys[i] = c.Key
			}
			return keys, nil
		}
		idx, err := parseNumbers(line, len(choices))
		if err != nil {
			fmt.Fprintln(p.out, "  ", err)
			continue
		}
		keys := make([]string, len(idx))
		for i, j := range idx {
			keys[i] = choices[j].Key
		}
		return keys, nil
	}
}

// One asks for exactly one choice; Enter picks the first.
func (p *Prompter) One(q string, choices []Choice) (string, error) {
	p.list(q, choices, "(Enter for 1)")
	for {
		line, err := p.readLine()
		if err != nil {
			return "", err
		}
		if line == "" {
			return choices[0].Key, nil
		}
		idx, err := parseNumbers(line, len(choices))
		if err != nil || len(idx) != 1 {
			fmt.Fprintf(p.out, "   pick one number between 1 and %d\n", len(choices))
			continue
		}
		return choices[idx[0]].Key, nil
	}
}

// Confirm asks a yes/no question.
func (p *Prompter) Confirm(q string, def bool) (bool, error) {
	hint := "[y/N]"
	if def {
		hint = "[Y/n]"
	}
	fmt.Fprintf(p.out, "\n? %s %s\n", q, hint)
	for {
		line, err := p.readLine()
		if err != nil {
			return false, err
		}
		switch strings.ToLower(line) {
		case "":
			return def, nil
		case "y", "yes":
			return true, nil
		case "n", "no":
			return false, nil
		}
		fmt.Fprintln(p.out, "   answer y or n")
	}
}
