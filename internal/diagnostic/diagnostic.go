package diagnostic

import "fmt"

type Severity int

const (
	Error   Severity = iota
	Warning Severity = iota
	Info    Severity = iota
)

type Diagnostic struct {
	File     string
	Line     int
	Col      int
	Message  string
	Hint     string
	Severity Severity
}

func (d Diagnostic) String() string {
	s := fmt.Sprintf("%s:%d:%d: %s", d.File, d.Line, d.Col, d.Message)
	if d.Hint != "" {
		s += "\n  hint: " + d.Hint
	}
	return s
}

type List []Diagnostic

func (l List) HasErrors() bool {
	for _, d := range l {
		if d.Severity == Error {
			return true
		}
	}
	return false
}

func (l *List) Add(d Diagnostic) {
	*l = append(*l, d)
}

func (l *List) AddError(file string, line, col int, message, hint string) {
	l.Add(Diagnostic{File: file, Line: line, Col: col, Message: message, Hint: hint, Severity: Error})
}

func (l *List) AddWarning(file string, line, col int, message, hint string) {
	l.Add(Diagnostic{File: file, Line: line, Col: col, Message: message, Hint: hint, Severity: Warning})
}

const (
	ExitOK    = 0
	ExitError = 1
	ExitUsage = 2
)
