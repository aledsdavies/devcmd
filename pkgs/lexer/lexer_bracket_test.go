package lexer

import (
	"testing"

	"github.com/aledsdavies/devcmd/pkgs/types"
)

func TestShellBracketStructures(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []tokenExpectation
	}{
		{
			name:  "simple parameter expansion",
			input: `test: echo ${VAR}`,
			expected: []tokenExpectation{
				{types.IDENTIFIER, "test"},
				{types.COLON, ":"},
				{types.SHELL_TEXT, "echo ${VAR}"},
				{types.EOF, ""},
			},
		},
		{
			name:  "parameter expansion with default",
			input: `test: echo ${VAR:-default}`,
			expected: []tokenExpectation{
				{types.IDENTIFIER, "test"},
				{types.COLON, ":"},
				{types.SHELL_TEXT, "echo ${VAR:-default}"},
				{types.EOF, ""},
			},
		},
		{
			name:  "nested parameter expansion",
			input: `test: echo ${VAR:-${DEFAULT}}`,
			expected: []tokenExpectation{
				{types.IDENTIFIER, "test"},
				{types.COLON, ":"},
				{types.SHELL_TEXT, "echo ${VAR:-${DEFAULT}}"},
				{types.EOF, ""},
			},
		},
		{
			name:  "command substitution with braces",
			input: `test: echo $(find . -name "*.go" -exec ls {} +)`,
			expected: []tokenExpectation{
				{types.IDENTIFIER, "test"},
				{types.COLON, ":"},
				{types.SHELL_TEXT, "echo $(find . -name \"*.go\" -exec ls {} +)"},
				{types.EOF, ""},
			},
		},
		{
			name:  "mixed parameter expansion and command substitution",
			input: `test: echo ${VAR:-$(date +%Y)}`,
			expected: []tokenExpectation{
				{types.IDENTIFIER, "test"},
				{types.COLON, ":"},
				{types.SHELL_TEXT, "echo ${VAR:-$(date +%Y)}"},
				{types.EOF, ""},
			},
		},
		{
			name:  "parameter expansion with @var function decorator",
			input: `test: echo ${@var(VAR):-default}`,
			expected: []tokenExpectation{
				{types.IDENTIFIER, "test"},
				{types.COLON, ":"},
				{types.SHELL_TEXT, "echo ${@var(VAR):-default}"},
				{types.EOF, ""},
			},
		},
		{
			name:  "array syntax",
			input: `test: echo ${ARRAY[0]}`,
			expected: []tokenExpectation{
				{types.IDENTIFIER, "test"},
				{types.COLON, ":"},
				{types.SHELL_TEXT, "echo ${ARRAY[0]}"},
				{types.EOF, ""},
			},
		},
		{
			name:  "brace expansion",
			input: `test: echo {a,b,c}`,
			expected: []tokenExpectation{
				{types.IDENTIFIER, "test"},
				{types.COLON, ":"},
				{types.SHELL_TEXT, "echo {a,b,c}"},
				{types.EOF, ""},
			},
		},
		{
			name:  "find with -exec and braces",
			input: `test: find . -name "*.txt" -exec rm {} \;`,
			expected: []tokenExpectation{
				{types.IDENTIFIER, "test"},
				{types.COLON, ":"},
				{types.SHELL_TEXT, "find . -name \"*.txt\" -exec rm {} \\;"},
				{types.EOF, ""},
			},
		},
		{
			name:  "complex shell with multiple bracket types",
			input: `test: for f in $(find . -name "*.go"); do echo ${f%.go}.bin; done`,
			expected: []tokenExpectation{
				{types.IDENTIFIER, "test"},
				{types.COLON, ":"},
				{types.SHELL_TEXT, "for f in $(find . -name \"*.go\"); do echo ${f%.go}.bin; done"},
				{types.EOF, ""},
			},
		},
		{
			name: "block command with shell brackets inside",
			input: `test: {
    echo ${VAR:-default}
    find . -exec ls {} +
}`,
			expected: []tokenExpectation{
				{types.IDENTIFIER, "test"},
				{types.COLON, ":"},
				{types.LBRACE, "{"},
				{types.SHELL_TEXT, "echo ${VAR:-default}"},
				{types.SHELL_TEXT, "find . -exec ls {} +"},
				{types.RBRACE, "}"},
				{types.EOF, ""},
			},
		},
		{
			name: "decorator with shell brackets in block",
			input: `test: @timeout(30s) {
    rsync -av ${SRC}/ ${DEST}/
}`,
			expected: []tokenExpectation{
				{types.IDENTIFIER, "test"},
				{types.COLON, ":"},
				{types.AT, "@"},
				{types.IDENTIFIER, "timeout"},
				{types.LPAREN, "("},
				{types.DURATION, "30s"},
				{types.RPAREN, ")"},
				{types.LBRACE, "{"},
				{types.SHELL_TEXT, "rsync -av ${SRC}/ ${DEST}/"},
				{types.RBRACE, "}"},
				{types.EOF, ""},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertTokens(t, tt.name, tt.input, tt.expected)
		})
	}
}
