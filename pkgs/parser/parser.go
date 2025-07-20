package parser

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/aledsdavies/devcmd/pkgs/ast"
	"github.com/aledsdavies/devcmd/pkgs/decorators"
	"github.com/aledsdavies/devcmd/pkgs/lexer"
	"github.com/aledsdavies/devcmd/pkgs/types"
)

// Parser implements a fast, spec-compliant recursive descent parser for the Devcmd language.
// It trusts the lexer to have correctly handled whitespace and tokenization, focusing
// purely on assembling the Abstract Syntax Tree (AST).
type Parser struct {
	input  string // The raw input string for accurate value slicing
	tokens []types.Token
	pos    int // current position in the token slice

	// errors is a slice of errors encountered during parsing.
	// This allows for better error reporting by collecting multiple errors.
	errors []string
}

// Parse tokenizes and parses the input from an io.Reader into a complete AST.
// It returns the Program node and any errors encountered.
func Parse(reader io.Reader) (*ast.Program, error) {
	// Read the input to store for error reporting
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("failed to read input: %w", err)
	}
	input := string(data)

	lex := lexer.New(strings.NewReader(input))
	p := &Parser{
		input:  input, // Store the raw input
		tokens: lex.TokenizeToSlice(),
	}
	program := p.parseProgram()

	if len(p.errors) > 0 {
		return nil, fmt.Errorf("parsing failed:\n- %s", strings.Join(p.errors, "\n- "))
	}
	return program, nil
}

// --- Main Parsing Logic ---

// parseProgram is the top-level entry point for parsing.
// It iterates through the tokens and parses all top-level statements.
// Program = { VariableDecl | VarGroup | CommandDecl }*
func (p *Parser) parseProgram() *ast.Program {
	program := &ast.Program{}

	for !p.isAtEnd() {
		p.skipWhitespaceAndComments()
		if p.isAtEnd() {
			break
		}

		switch p.current().Type {
		case types.VAR:
			if p.peek().Type == types.LPAREN {
				varGroup, err := p.parseVarGroup()
				if err != nil {
					p.addError(err)
					p.synchronize()
				} else {
					program.VarGroups = append(program.VarGroups, *varGroup)
				}
			} else {
				varDecl, err := p.parseVariableDecl()
				if err != nil {
					p.addError(err)
					p.synchronize()
				} else {
					program.Variables = append(program.Variables, *varDecl)
				}
			}
		case types.IDENTIFIER, types.WATCH, types.STOP:
			// A command can start with a name (IDENTIFIER), a keyword (WATCH/STOP),
			// or a decorator (@).
			cmd, err := p.parseCommandDecl()
			if err != nil {
				p.addError(err)
				p.synchronize()
			} else {
				program.Commands = append(program.Commands, *cmd)
			}
		default:
			p.addError(fmt.Errorf("unexpected token %s, expected a top-level declaration (var, command)", p.current().Type))
			p.synchronize()
		}
	}

	return program
}

// parseCommandDecl parses a full command declaration.
// CommandDecl = { Decorator }* [ "watch" | "stop" ] IDENTIFIER ":" CommandBody
func (p *Parser) parseCommandDecl() (*ast.CommandDecl, error) {
	startPos := p.current()

	// 1. Parse command type (watch, stop, or regular)
	cmdType := ast.Command
	var typeToken *types.Token
	if p.match(types.WATCH) {
		cmdType = ast.WatchCommand
		token := p.current()
		typeToken = &token
		p.advance()
	} else if p.match(types.STOP) {
		cmdType = ast.StopCommand
		token := p.current()
		typeToken = &token
		p.advance()
	}

	// 2. Parse command name
	nameToken, err := p.consume(types.IDENTIFIER, "expected command name")
	if err != nil {
		return nil, err
	}
	name := nameToken.Value

	// 3. Parse colon
	colonToken, err := p.consume(types.COLON, "expected ':' after command name")
	if err != nil {
		return nil, err
	}

	// 4. Parse command body (this will handle post-colon decorators and syntax sugar)
	body, err := p.parseCommandBody()
	if err != nil {
		return nil, err
	}

	return &ast.CommandDecl{
		Name:       name,
		Type:       cmdType,
		Body:       *body,
		Pos:        ast.Position{Line: startPos.Line, Column: startPos.Column},
		TypeToken:  typeToken,
		NameToken:  nameToken,
		ColonToken: colonToken,
	}, nil
}

// parseCommandBody parses the content after the command's colon.
// It handles the syntax sugar for simple vs. block commands.
// **FIXED**: Now properly implements syntax sugar equivalence as per spec.
// CommandBody = "{" CommandContent "}" | DecoratorSugar | CommandContent
func (p *Parser) parseCommandBody() (*ast.CommandBody, error) {
	startPos := p.current()

	// **FIXED**: Check for decorator syntax sugar: @decorator(args) { ... }
	// This should be equivalent to: { @decorator(args) { ... } }
	if p.match(types.AT) {
		// Save position in case we need to backtrack
		savedPos := p.pos

		// Try to parse a single decorator after the colon
		decorator, err := p.parseDecorator()
		if err != nil {
			return nil, err
		}

		// After decorators, we expect either:
		// 1. A block { ... } (syntax sugar - should be treated as IsBlock=true)
		// 2. Simple shell content (only valid for function decorators)

		if p.match(types.LBRACE) {
			// **SYNTAX SUGAR**: @decorator(args) { ... } becomes { @decorator(args) { ... } }
			openBrace, _ := p.consume(types.LBRACE, "") // already checked

			// Parse content differently based on decorator type
			switch d := decorator.(type) {
			case *ast.BlockDecorator:
				blockContent, err := p.parseBlockContent() // Parse multiple content items
				if err != nil {
					return nil, err
				}
				closeBrace, err := p.consume(types.RBRACE, "expected '}' to close command block")
				if err != nil {
					return nil, err
				}
				d.Content = blockContent
				return &ast.CommandBody{
					Content:    []ast.CommandContent{d},
					Pos:        ast.Position{Line: startPos.Line, Column: startPos.Column},
					OpenBrace:  &openBrace,
					CloseBrace: &closeBrace,
				}, nil
			case *ast.PatternDecorator:
				// For pattern decorators, parse pattern branches directly
				patterns, err := p.parsePatternBranchesInBlock()
				if err != nil {
					return nil, err
				}
				closeBrace, err := p.consume(types.RBRACE, "expected '}' to close pattern block")
				if err != nil {
					return nil, err
				}
				d.Patterns = patterns
				return &ast.CommandBody{
					Content:    []ast.CommandContent{d},
					Pos:        ast.Position{Line: startPos.Line, Column: startPos.Column},
					OpenBrace:  &openBrace,
					CloseBrace: &closeBrace,
				}, nil
			default:
				return nil, fmt.Errorf("unexpected decorator type in block context")
			}
		} else {
			// Decorator without braces - check if it's a function decorator
			if _, ok := decorator.(*ast.FunctionDecorator); !ok {
				// Block decorators must be followed by braces
				return nil, fmt.Errorf("expected '{' after block decorator(s) (at %d:%d, got %s)",
					p.current().Line, p.current().Column, p.current().Type)
			}

			// All function decorators - backtrack and parse as shell content
			p.pos = savedPos
			content, err := p.parseCommandContent(false)
			if err != nil {
				return nil, err
			}

			// **SYNTAX SUGAR NORMALIZATION**: Simple commands with only function decorators
			// should have the same AST structure as simple commands without decorators
			return &ast.CommandBody{
				Content: []ast.CommandContent{content},
				Pos:     ast.Position{Line: startPos.Line, Column: startPos.Column},
			}, nil
		}
	}

	// Explicit block: { ... }
	if p.match(types.LBRACE) {
		openBrace, _ := p.consume(types.LBRACE, "") // already checked
		contentItems, err := p.parseBlockContent()  // Parse multiple content items
		if err != nil {
			return nil, err
		}
		closeBrace, err := p.consume(types.RBRACE, "expected '}' to close command block")
		if err != nil {
			return nil, err
		}

		// **SYNTAX SUGAR NORMALIZATION**: All equivalent forms produce same AST structure
		// Both "build: npm run build" and "build: { npm run build }" are now identical
		if p.isSimpleShellContent(contentItems) {
			return &ast.CommandBody{
				Content: contentItems,
				Pos:     ast.Position{Line: startPos.Line, Column: startPos.Column},
				// Note: No brace tokens stored for simple commands (canonical form)
			}, nil
		}

		return &ast.CommandBody{
			Content:    contentItems, // Already a slice
			Pos:        ast.Position{Line: startPos.Line, Column: startPos.Column},
			OpenBrace:  &openBrace,
			CloseBrace: &closeBrace,
		}, nil
	}

	// Simple command (no braces, ends at newline)
	content, err := p.parseCommandContent(false) // Pass inBlock=false
	if err != nil {
		return nil, err
	}
	return &ast.CommandBody{
		Content: []ast.CommandContent{content},
		Pos:     ast.Position{Line: startPos.Line, Column: startPos.Column},
	}, nil
}

// isSimpleShellContent checks if content items represent simple shell content
// that should be normalized to canonical form (IsBlock=false)
func (p *Parser) isSimpleShellContent(contentItems []ast.CommandContent) bool {
	// Must be exactly one content item
	if len(contentItems) != 1 {
		return false
	}

	// Must be shell content without decorators
	if shell, ok := contentItems[0].(*ast.ShellContent); ok {
		// Check if it contains only text parts or function decorators (no block decorators)
		for _, part := range shell.Parts {
			if funcDecorator, ok := part.(*ast.FunctionDecorator); ok {
				// Function decorators are allowed in simple content
				if !decorators.IsFunctionDecorator(funcDecorator.Name) {
					return false
				}
			}
		}
		return true
	}

	return false
}

// parseCommandContent parses the actual content of a command, which can be
// shell text, decorators, or pattern content.
// It is context-aware via the `inBlock` parameter.
func (p *Parser) parseCommandContent(inBlock bool) (ast.CommandContent, error) {
	// Check for pattern decorators (@when, @try)
	if p.isPatternDecorator() {
		return p.parsePatternContent()
	}

	// Check for block decorators
	if p.isBlockDecorator() {
		decorator, err := p.parseDecorator()
		if err != nil {
			return nil, err
		}

		// Handle different decorator types
		switch d := decorator.(type) {
		case *ast.BlockDecorator:
			// Parse the block content for block decorators
			if p.match(types.LBRACE) {
				p.advance() // consume '{'
				contentItems, err := p.parseBlockContent()
				if err != nil {
					return nil, err
				}
				_, err = p.consume(types.RBRACE, "expected '}' after block decorator content")
				if err != nil {
					return nil, err
				}
				d.Content = contentItems
			} else {
				return nil, fmt.Errorf("expected '{' after block decorator @%s", d.Name)
			}
			return d, nil
		case *ast.PatternDecorator:
			// Pattern decorators are handled separately
			return nil, fmt.Errorf("pattern decorators should be handled by parsePatternContent")
		default:
			return nil, fmt.Errorf("unexpected decorator type in block context")
		}
	}

	// Otherwise, it must be shell content.
	return p.parseShellContent(inBlock)
}

// parsePatternContent parses pattern-matching decorator content (@when, @try)
// This handles syntax like: @when(VAR) { pattern: command; pattern: command }
func (p *Parser) parsePatternContent() (*ast.PatternDecorator, error) {
	startPos := p.current()

	// Parse @ symbol
	atToken, err := p.consume(types.AT, "expected '@' to start pattern decorator")
	if err != nil {
		return nil, err
	}

	// Parse decorator name
	nameToken, err := p.consume(types.IDENTIFIER, "expected decorator name after '@'")
	if err != nil {
		return nil, err
	}
	decoratorName := nameToken.Value

	// Step 1: Check if decorator exists in registry and is a pattern decorator
	decorator, decoratorType, err := decorators.GetAny(decoratorName)
	if err != nil || decoratorType != decorators.PatternType {
		return nil, fmt.Errorf("unknown pattern decorator @%s", decoratorName)
	}

	// Step 2: Get parameter schema
	paramSchema := decorator.ParameterSchema()

	// Parse arguments if present
	var params []ast.NamedParameter
	if p.match(types.LPAREN) {
		params, err = p.parseParameterList(paramSchema)
		if err != nil {
			return nil, err
		}
		_, err = p.consume(types.RPAREN, "expected ')' after decorator arguments")
		if err != nil {
			return nil, err
		}
	}

	// Step 3: Run decorator's validate method
	ctx := &decorators.ExecutionContext{}
	if err := decorator.Validate(ctx, params); err != nil {
		return nil, fmt.Errorf("invalid pattern decorator usage @%s: %w", decoratorName, err)
	}

	// Expect opening brace
	_, err = p.consume(types.LBRACE, "expected '{' after pattern decorator")
	if err != nil {
		return nil, err
	}

	// Parse pattern branches
	var patterns []ast.PatternBranch
	for !p.match(types.RBRACE) && !p.isAtEnd() {
		p.skipWhitespaceAndComments()
		if p.match(types.RBRACE) {
			break
		}

		branch, err := p.parsePatternBranch()
		if err != nil {
			return nil, err
		}
		patterns = append(patterns, *branch)
		p.skipWhitespaceAndComments()
	}

	// Expect closing brace
	_, err = p.consume(types.RBRACE, "expected '}' to close pattern block")
	if err != nil {
		return nil, err
	}

	return &ast.PatternDecorator{
		Name:      decoratorName,
		Args:      params,
		Patterns:  patterns,
		Pos:       ast.Position{Line: startPos.Line, Column: startPos.Column},
		AtToken:   atToken,
		NameToken: nameToken,
	}, nil
}

// parsePatternBranch parses a single pattern branch: pattern: command or pattern: { commands }
// **FIXED**: Now properly handles multiple commands per pattern branch
func (p *Parser) parsePatternBranch() (*ast.PatternBranch, error) {
	startPos := p.current()

	// Parse pattern (identifier or wildcard)
	var pattern ast.Pattern
	if p.match(types.IDENTIFIER) {
		token := p.current()
		p.advance()

		// Check if this is the "default" wildcard pattern
		if token.Value == "default" {
			pattern = &ast.WildcardPattern{
				Pos:   ast.Position{Line: token.Line, Column: token.Column},
				Token: token,
			}
		} else {
			pattern = &ast.IdentifierPattern{
				Name:  token.Value,
				Pos:   ast.Position{Line: token.Line, Column: token.Column},
				Token: token,
			}
		}
	} else {
		return nil, fmt.Errorf("expected pattern identifier, got %s", p.current().Type)
	}

	// Parse colon
	colonToken, err := p.consume(types.COLON, "expected ':' after pattern")
	if err != nil {
		return nil, err
	}

	// **FIXED**: Parse command content - handle both single commands and blocks
	var commands []ast.CommandContent

	// Check if pattern branch has explicit block syntax: pattern: { ... }
	if p.match(types.LBRACE) {
		p.advance() // consume '{'
		blockCommands, err := p.parseBlockContent()
		if err != nil {
			return nil, err
		}
		_, err = p.consume(types.RBRACE, "expected '}' to close pattern branch block")
		if err != nil {
			return nil, err
		}
		commands = blockCommands
	} else {
		// Single command without braces: pattern: command
		content, err := p.parseCommandContent(true) // Pattern branches are always in block context
		if err != nil {
			return nil, err
		}
		commands = []ast.CommandContent{content}
	}

	return &ast.PatternBranch{
		Pattern:    pattern,
		Commands:   commands, // Now properly supports multiple commands
		Pos:        ast.Position{Line: startPos.Line, Column: startPos.Column},
		ColonToken: colonToken,
	}, nil
}

// parseBlockContent parses multiple content items within a block
// **FIXED**: Now properly handles multiple consecutive SHELL_TEXT tokens as separate commands
func (p *Parser) parseBlockContent() ([]ast.CommandContent, error) {
	var contentItems []ast.CommandContent

	for !p.match(types.RBRACE) && !p.isAtEnd() {
		p.skipWhitespaceAndComments()
		if p.match(types.RBRACE) {
			break
		}

		// Check for pattern decorators (@when, @try)
		if p.isPatternDecorator() {
			pattern, err := p.parsePatternContent()
			if err != nil {
				return nil, err
			}
			contentItems = append(contentItems, pattern)
			continue
		}

		// Check for block decorators
		if p.isBlockDecorator() {
			decorator, err := p.parseDecorator()
			if err != nil {
				return nil, err
			}

			// Handle different decorator types
			switch d := decorator.(type) {
			case *ast.BlockDecorator:
				// Parse the block content for block decorators
				if p.match(types.LBRACE) {
					p.advance() // consume '{'
					nestedContent, err := p.parseBlockContent()
					if err != nil {
						return nil, err
					}
					_, err = p.consume(types.RBRACE, "expected '}' after block decorator content")
					if err != nil {
						return nil, err
					}
					d.Content = nestedContent
				} else {
					// Parse single shell content
					content, err := p.parseShellContent(true)
					if err != nil {
						return nil, err
					}
					d.Content = []ast.CommandContent{content}
				}
				contentItems = append(contentItems, d)
			case *ast.PatternDecorator:
				// Pattern decorators shouldn't appear here
				return nil, fmt.Errorf("pattern decorators should be handled separately")
			default:
				return nil, fmt.Errorf("unexpected decorator type in block context")
			}
			continue
		}

		// **CRITICAL FIX**: Parse consecutive SHELL_TEXT tokens as separate commands
		// This implements the spec requirement: "newlines create multiple commands everywhere"
		if p.match(types.SHELL_TEXT) {
			shellContent, err := p.parseShellContent(true)
			if err != nil {
				return nil, err
			}

			// Only add non-empty shell content
			if len(shellContent.Parts) > 0 {
				contentItems = append(contentItems, shellContent)
			}
			continue
		}

		// If we get here, we have an unexpected token
		break
	}

	return contentItems, nil
}

// parseShellContent parses a single shell content item (one SHELL_TEXT token)
// **UPDATED**: Now parses only one SHELL_TEXT token to create separate content items
func (p *Parser) parseShellContent(inBlock bool) (*ast.ShellContent, error) {
	startPos := p.current()
	var parts []ast.ShellPart

	// Parse only one SHELL_TEXT token at a time
	if p.match(types.SHELL_TEXT) {
		shellToken := p.current()
		p.advance()

		extractedParts, err := p.extractInlineDecorators(shellToken.Value)
		if err != nil {
			return nil, err
		}

		parts = append(parts, extractedParts...)
	}

	return &ast.ShellContent{
		Parts: parts,
		Pos:   ast.Position{Line: startPos.Line, Column: startPos.Column},
	}, nil
}

// extractInlineDecorators extracts function decorators from shell text using decorators registry validation
func (p *Parser) extractInlineDecorators(shellText string) ([]ast.ShellPart, error) {
	var parts []ast.ShellPart
	textStart := 0

	for i := 0; i < len(shellText); {
		// Look for @ symbol
		atPos := strings.IndexByte(shellText[i:], '@')
		if atPos == -1 {
			// No more @ symbols, add remaining text if any
			if textStart < len(shellText) {
				parts = append(parts, &ast.TextPart{Text: shellText[textStart:]})
			}
			break
		}

		// Absolute position of @
		absAtPos := i + atPos

		// Try to extract decorator starting at @
		decorator, newPos, found := p.extractFunctionDecorator(shellText, absAtPos)
		if found {
			// Add any text before the decorator
			if absAtPos > textStart {
				parts = append(parts, &ast.TextPart{Text: shellText[textStart:absAtPos]})
			}
			// Add the decorator
			parts = append(parts, decorator)
			// Update positions
			i = newPos
			textStart = newPos
		} else {
			// Not a valid function decorator, continue scanning after this @
			i = absAtPos + 1
		}
	}

	return parts, nil
}

// extractFunctionDecorator extracts a function decorator starting at position i using unified decorator approach
// Returns the decorator, new position, and whether a decorator was found
func (p *Parser) extractFunctionDecorator(shellText string, i int) (*ast.FunctionDecorator, int, bool) {
	if i >= len(shellText) || shellText[i] != '@' {
		return nil, i, false
	}

	// Look for decorator name after @
	start := i + 1 // Skip @
	nameStart := start

	// First character must be a letter
	if start >= len(shellText) || !isLetter(rune(shellText[start])) {
		return nil, i, false
	}
	start++

	// Rest can be letters, digits, underscore, or hyphen
	for start < len(shellText) && (isLetter(rune(shellText[start])) || isDigit(rune(shellText[start])) || shellText[start] == '_' || shellText[start] == '-') {
		start++
	}

	decoratorName := shellText[nameStart:start]

	// Step 1: Check if decorator exists in registry and is a function decorator
	decorator, decoratorType, err := decorators.GetAny(decoratorName)
	if err != nil || decoratorType != decorators.FunctionType {
		return nil, i, false
	}

	// Look for opening parenthesis
	if start >= len(shellText) || shellText[start] != '(' {
		// Function decorators require parentheses
		return nil, i, false
	}

	// Find matching closing parenthesis
	start++ // Skip opening (
	parenCount := 1
	argStart := start

	for start < len(shellText) && parenCount > 0 {
		switch shellText[start] {
		case '(':
			parenCount++
		case ')':
			parenCount--
		}
		start++
	}

	if parenCount != 0 {
		// Unmatched parentheses
		return nil, i, false
	}

	// Extract argument text (between parentheses)
	argEnd := start - 1 // Position of closing ')'
	argText := shellText[argStart:argEnd]

	// Step 2: Get parameter schema and parse simple arguments
	paramSchema := decorator.ParameterSchema()
	var params []ast.NamedParameter

	if strings.TrimSpace(argText) != "" {
		trimmed := strings.TrimSpace(argText)

		// For inline decorators, we'll use simple parsing (no named parameters for now)
		var value ast.Expression

		// Handle quoted strings
		if (strings.HasPrefix(trimmed, `"`) && strings.HasSuffix(trimmed, `"`)) ||
			(strings.HasPrefix(trimmed, `'`) && strings.HasSuffix(trimmed, `'`)) ||
			(strings.HasPrefix(trimmed, "`") && strings.HasSuffix(trimmed, "`")) {
			// String literal - remove quotes
			unquoted := trimmed[1 : len(trimmed)-1]
			value = &ast.StringLiteral{Value: unquoted}
		} else {
			// Identifier
			value = &ast.Identifier{Name: trimmed}
		}

		// Use first parameter name from schema if available
		var paramName string
		if len(paramSchema) > 0 {
			paramName = paramSchema[0].Name
		} else {
			paramName = "arg0"
		}

		params = append(params, ast.NamedParameter{
			Name:  paramName,
			Value: value,
			Pos:   ast.Position{Line: 1, Column: i + 1},
		})
	}

	// Step 3: Validate (minimal validation for inline decorators)
	ctx := &decorators.ExecutionContext{}
	if err := decorator.Validate(ctx, params); err != nil {
		return nil, i, false // Invalid decorator usage
	}

	functionDecorator := &ast.FunctionDecorator{
		Name: decoratorName,
		Args: params,
		Pos:  ast.Position{Line: 1, Column: i + 1}, // Approximate position
	}

	return functionDecorator, start, true
}

// Helper functions for character classification
func isLetter(ch rune) bool {
	return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z')
}

func isDigit(ch rune) bool {
	return ch >= '0' && ch <= '9'
}

// --- Expression and Literal Parsing ---

// --- Variable Parsing ---

// parseVariableDecl parses a variable declaration.
// **SPEC COMPLIANCE**: Now enforces that values must be string, number, duration, or boolean literals
func (p *Parser) parseVariableDecl() (*ast.VariableDecl, error) {
	startPos := p.current()
	_, err := p.consume(types.VAR, "expected 'var'")
	if err != nil {
		return nil, err
	}

	name, err := p.consume(types.IDENTIFIER, "expected variable name")
	if err != nil {
		return nil, err
	}
	_, err = p.consume(types.EQUALS, "expected '=' after variable name")
	if err != nil {
		return nil, err
	}

	// Parse variable value - must be a literal (string, number, duration, or boolean)
	value, err := p.parseVariableValue()
	if err != nil {
		return nil, err
	}

	return &ast.VariableDecl{
		Name:      name.Value,
		Value:     value,
		Pos:       ast.Position{Line: startPos.Line, Column: startPos.Column},
		NameToken: name,
	}, nil
}

// parseVariableValue parses variable values, now restricted to literals only.
// **SPEC COMPLIANCE**: Only allows the 4 supported types: string, number, duration, boolean
func (p *Parser) parseVariableValue() (ast.Expression, error) {
	startToken := p.current()

	// Handle standard literals only - no unquoted strings allowed
	switch startToken.Type {
	case types.STRING:
		p.advance()
		return &ast.StringLiteral{Value: startToken.Value, Raw: startToken.Raw, StringToken: startToken}, nil
	case types.NUMBER:
		p.advance()
		return &ast.NumberLiteral{Value: startToken.Value, Token: startToken}, nil
	case types.DURATION:
		p.advance()
		return &ast.DurationLiteral{Value: startToken.Value, Token: startToken}, nil
	case types.BOOLEAN:
		p.advance()
		return &ast.BooleanLiteral{Value: startToken.Value == "true", Token: startToken}, nil
	default:
		// **SPEC COMPLIANCE**: No longer allow arbitrary unquoted strings
		return nil, fmt.Errorf("variable value must be a quoted string, number, duration, or boolean literal at line %d, col %d (got %s)",
			startToken.Line, startToken.Column, startToken.Type)
	}
}

func (p *Parser) parseVarGroup() (*ast.VarGroup, error) {
	startPos := p.current()
	_, err := p.consume(types.VAR, "expected 'var'")
	if err != nil {
		return nil, err
	}
	openParen, err := p.consume(types.LPAREN, "expected '(' for var group")
	if err != nil {
		return nil, err
	}

	var variables []ast.VariableDecl
	for !p.match(types.RPAREN) && !p.isAtEnd() {
		p.skipWhitespaceAndComments()
		if p.match(types.RPAREN) {
			break
		}
		if p.current().Type != types.IDENTIFIER {
			p.addError(fmt.Errorf("expected variable name inside var group, got %s", p.current().Type))
			p.synchronize()
			continue
		}

		varDecl, err := p.parseGroupedVariableDecl()
		if err != nil {
			return nil, err // Be strict inside var groups
		}
		variables = append(variables, *varDecl)
		p.skipWhitespaceAndComments()
	}

	closeParen, err := p.consume(types.RPAREN, "expected ')' to close var group")
	if err != nil {
		return nil, err
	}

	return &ast.VarGroup{
		Variables:  variables,
		Pos:        ast.Position{Line: startPos.Line, Column: startPos.Column},
		OpenParen:  openParen,
		CloseParen: closeParen,
	}, nil
}

// parseGroupedVariableDecl is a helper for parsing `NAME = VALUE` lines within a `var (...)` block.
func (p *Parser) parseGroupedVariableDecl() (*ast.VariableDecl, error) {
	name, err := p.consume(types.IDENTIFIER, "expected variable name")
	if err != nil {
		return nil, err
	}
	_, err = p.consume(types.EQUALS, "expected '=' after variable name")
	if err != nil {
		return nil, err
	}

	// Use the same restricted value parsing logic.
	value, err := p.parseVariableValue()
	if err != nil {
		return nil, err
	}

	return &ast.VariableDecl{
		Name:      name.Value,
		Value:     value,
		Pos:       ast.Position{Line: name.Line, Column: name.Column},
		NameToken: name,
	}, nil
}

// --- Decorator Parsing ---

// parseDecorator parses a single decorator and returns the appropriate AST node type
func (p *Parser) parseDecorator() (ast.CommandContent, error) {
	startPos := p.current()
	atToken, _ := p.consume(types.AT, "expected '@'")

	// Get decorator name
	var nameToken types.Token
	var err error

	if p.current().Type == types.IDENTIFIER {
		nameToken, err = p.consume(types.IDENTIFIER, "expected decorator name")
	} else {
		// Handle special cases where keywords appear as decorator names
		nameToken = p.current()
		if !p.isValidDecoratorName(nameToken) {
			return nil, fmt.Errorf("expected decorator name, got %s", nameToken.Type)
		}
		p.advance()
	}

	if err != nil {
		return nil, err
	}

	decoratorName := nameToken.Value
	if nameToken.Type != types.IDENTIFIER {
		decoratorName = strings.ToLower(nameToken.Value)
	}

	// Step 1: Check if decorator exists in registry
	decorator, decoratorType, err := decorators.GetAny(decoratorName)
	if err != nil {
		return nil, fmt.Errorf("unknown decorator @%s", decoratorName)
	}

	// Step 2: Get parameter schema from decorator
	paramSchema := decorator.ParameterSchema()

	// Step 3: Parse parameters according to schema
	var params []ast.NamedParameter
	if p.match(types.LPAREN) {
		p.advance() // consume '('
		params, err = p.parseParameterList(paramSchema)
		if err != nil {
			return nil, err
		}
		_, err = p.consume(types.RPAREN, "expected ')' after decorator arguments")
		if err != nil {
			return nil, err
		}
	}

	// Step 4: Run decorator's validate method
	ctx := &decorators.ExecutionContext{} // Create minimal context for validation
	if err := decorator.Validate(ctx, params); err != nil {
		return nil, fmt.Errorf("invalid decorator usage @%s: %w", decoratorName, err)
	}

	// Step 5: Create appropriate AST node based on decorator type
	switch decoratorType {
	case decorators.FunctionType:
		return &ast.FunctionDecorator{
			Name:      decoratorName,
			Args:      params,
			Pos:       ast.Position{Line: startPos.Line, Column: startPos.Column},
			AtToken:   atToken,
			NameToken: nameToken,
		}, nil
	case decorators.BlockType:
		return &ast.BlockDecorator{
			Name:      decoratorName,
			Args:      params,
			Content:   nil, // Will be filled in by caller
			Pos:       ast.Position{Line: startPos.Line, Column: startPos.Column},
			AtToken:   atToken,
			NameToken: nameToken,
		}, nil
	case decorators.PatternType:
		return &ast.PatternDecorator{
			Name:      decoratorName,
			Args:      params,
			Patterns:  nil, // Will be filled in by caller
			Pos:       ast.Position{Line: startPos.Line, Column: startPos.Column},
			AtToken:   atToken,
			NameToken: nameToken,
		}, nil
	default:
		return nil, fmt.Errorf("unknown decorator type for @%s", decoratorName)
	}
}

// parseParameterList parses a comma-separated list of named parameters using the decorator's schema
func (p *Parser) parseParameterList(paramSchema []decorators.ParameterSchema) ([]ast.NamedParameter, error) {
	var params []ast.NamedParameter
	if p.match(types.RPAREN) {
		return params, nil // No parameters
	}

	positionalIndex := 0

	for {
		param, err := p.parseParameter(paramSchema, &positionalIndex)
		if err != nil {
			return nil, err
		}
		params = append(params, param)

		if !p.match(types.COMMA) {
			break
		}
		p.advance() // consume ','
	}
	return params, nil
}

// parseParameter parses a single parameter (either named or positional) using the schema
func (p *Parser) parseParameter(paramSchema []decorators.ParameterSchema, positionalIndex *int) (ast.NamedParameter, error) {
	startPos := p.current()

	// Check if this is a named parameter (identifier = value)
	if p.current().Type == types.IDENTIFIER && p.peek().Type == types.EQUALS {
		// Named parameter
		nameToken, err := p.consume(types.IDENTIFIER, "expected parameter name")
		if err != nil {
			return ast.NamedParameter{}, err
		}
		equalsToken, err := p.consume(types.EQUALS, "expected '=' after parameter name")
		if err != nil {
			return ast.NamedParameter{}, err
		}

		value, err := p.parseValue()
		if err != nil {
			return ast.NamedParameter{}, err
		}

		return ast.NamedParameter{
			Name:        nameToken.Value,
			Value:       value,
			Pos:         ast.Position{Line: startPos.Line, Column: startPos.Column},
			NameToken:   &nameToken,
			EqualsToken: &equalsToken,
		}, nil
	} else {
		// Positional parameter
		value, err := p.parseValue()
		if err != nil {
			return ast.NamedParameter{}, err
		}

		// Get parameter name from schema at current position
		var paramName string
		if *positionalIndex < len(paramSchema) {
			paramName = paramSchema[*positionalIndex].Name
		} else {
			paramName = fmt.Sprintf("arg%d", *positionalIndex)
		}
		*positionalIndex++

		return ast.NamedParameter{
			Name:  paramName,
			Value: value,
			Pos:   ast.Position{Line: startPos.Line, Column: startPos.Column},
			// NameToken and EqualsToken are nil for positional parameters
		}, nil
	}
}

// parseValue parses a literal value (string, number, duration, boolean, identifier)
func (p *Parser) parseValue() (ast.Expression, error) {
	switch p.current().Type {
	case types.STRING:
		tok := p.current()
		p.advance()
		return &ast.StringLiteral{Value: tok.Value, Raw: tok.Raw, StringToken: tok}, nil
	case types.NUMBER:
		tok := p.current()
		p.advance()
		return &ast.NumberLiteral{Value: tok.Value, Token: tok}, nil
	case types.DURATION:
		tok := p.current()
		p.advance()
		return &ast.DurationLiteral{Value: tok.Value, Token: tok}, nil
	case types.BOOLEAN:
		tok := p.current()
		p.advance()
		boolValue := tok.Value == "true"
		return &ast.BooleanLiteral{Value: boolValue, Raw: tok.Value, Token: tok}, nil
	case types.IDENTIFIER:
		tok := p.current()
		p.advance()
		return &ast.Identifier{Name: tok.Value, Token: tok}, nil
	default:
		return nil, fmt.Errorf("expected value (string, number, duration, boolean, or identifier), got %s", p.current().Type)
	}
}

// isValidDecoratorName checks if a token can be used as a decorator name
func (p *Parser) isValidDecoratorName(token types.Token) bool {
	switch token.Type {
	case types.IDENTIFIER:
		return true
	case types.VAR:
		// "var" can be used as a decorator name for @var()
		return true
	default:
		return false
	}
}

// parsePatternBranchesInBlock parses pattern branches directly from the token stream
func (p *Parser) parsePatternBranchesInBlock() ([]ast.PatternBranch, error) {
	var patterns []ast.PatternBranch

	for !p.match(types.RBRACE) && !p.isAtEnd() {
		p.skipWhitespaceAndComments()
		if p.match(types.RBRACE) {
			break
		}

		branch, err := p.parsePatternBranch()
		if err != nil {
			return nil, err
		}
		patterns = append(patterns, *branch)

		p.skipWhitespaceAndComments()
	}

	return patterns, nil
}

// --- Utility and Helper Methods ---

func (p *Parser) advance() types.Token {
	if !p.isAtEnd() {
		p.pos++
	}
	return p.previous()
}

func (p *Parser) current() types.Token  { return p.tokens[p.pos] }
func (p *Parser) previous() types.Token { return p.tokens[p.pos-1] }
func (p *Parser) peek() types.Token     { return p.tokens[p.pos+1] }

func (p *Parser) isAtEnd() bool { return p.current().Type == types.EOF }

func (p *Parser) match(types ...types.TokenType) bool {
	for _, t := range types {
		if p.current().Type == t {
			return true
		}
	}
	return false
}

// formatError creates a detailed error message with source context
func (p *Parser) formatError(message string, token types.Token) error {
	lines := strings.Split(p.input, "\n")
	lineNum := token.Line
	colNum := token.Column

	var errorMsg strings.Builder
	errorMsg.WriteString(fmt.Sprintf("parsing failed:\n- %s\n\n", message))

	// Show context around the error
	startLine := max(1, lineNum-1)
	endLine := min(len(lines), lineNum+1)

	maxLineNumWidth := len(strconv.Itoa(endLine))

	for i := startLine; i <= endLine; i++ {
		lineContent := ""
		if i <= len(lines) {
			lineContent = lines[i-1] // lines are 0-indexed, but line numbers are 1-indexed
		}

		lineNumStr := fmt.Sprintf("%*d", maxLineNumWidth, i)

		if i == lineNum {
			// This is the error line - highlight it
			errorMsg.WriteString(fmt.Sprintf(" --> %s | %s\n", lineNumStr, lineContent))

			// Add pointer to the exact column
			padding := strings.Repeat(" ", maxLineNumWidth+3+colNum-1) // account for " --> " and column position
			errorMsg.WriteString(fmt.Sprintf("     %s | %s^\n", strings.Repeat(" ", maxLineNumWidth), padding))
			errorMsg.WriteString(fmt.Sprintf("     %s | %s%s\n", strings.Repeat(" ", maxLineNumWidth), padding, "unexpected "+token.Type.String()))
		} else {
			// Context line
			errorMsg.WriteString(fmt.Sprintf("     %s | %s\n", lineNumStr, lineContent))
		}
	}

	return fmt.Errorf("%s", errorMsg.String())
}

// max returns the larger of two integers
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// min returns the smaller of two integers
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (p *Parser) consume(t types.TokenType, message string) (types.Token, error) {
	if p.match(t) {
		tok := p.current()
		p.advance()
		return tok, nil
	}
	return types.Token{}, p.formatError(message, p.current())
}

func (p *Parser) skipWhitespaceAndComments() {
	// NEWLINE tokens no longer exist - they're handled as whitespace by lexer
	for p.match(types.COMMENT, types.MULTILINE_COMMENT) {
		p.advance()
	}
}

// isPatternDecorator checks if the current position starts a pattern decorator.
func (p *Parser) isPatternDecorator() bool {
	if p.current().Type != types.AT {
		return false
	}
	if p.pos+1 < len(p.tokens) {
		nextToken := p.tokens[p.pos+1]

		if nextToken.Type == types.IDENTIFIER {
			// Use the decorator registry to check for pattern decorators
			return decorators.IsPatternDecorator(nextToken.Value)
		}
	}
	return false
}

// isBlockDecorator checks if the current position starts a block decorator.
func (p *Parser) isBlockDecorator() bool {
	if p.current().Type != types.AT {
		return false
	}
	if p.pos+1 < len(p.tokens) {
		nextToken := p.tokens[p.pos+1]
		var name string

		if nextToken.Type == types.IDENTIFIER {
			name = nextToken.Value
		} else {
			return false
		}

		// Use the decorator registry to check for block decorators
		return decorators.IsBlockDecorator(name)
	}
	return false
}

// addError records an error and allows parsing to continue.
func (p *Parser) addError(err error) {
	p.errors = append(p.errors, err.Error())
}

// synchronize advances the parser until it finds a probable statement boundary,
// allowing it to recover from an error and report more than one error per file.
func (p *Parser) synchronize() {
	p.advance()
	for !p.isAtEnd() {
		// NEWLINE tokens no longer exist - removed synchronization point
		// A new top-level keyword is also a good place.
		switch p.current().Type {
		case types.VAR, types.WATCH, types.STOP:
			return
		}
		p.advance()
	}
}
