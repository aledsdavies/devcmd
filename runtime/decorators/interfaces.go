package decorators

import (
	"github.com/aledsdavies/devcmd/core/ast"
	"github.com/aledsdavies/devcmd/runtime/execution"
)

// ParameterSchema describes a decorator parameter
type ParameterSchema struct {
	Name        string             // Parameter name (e.g., "key", "default")
	Type        ast.ExpressionType // Parameter type (StringType, NumberType, etc.)
	Required    bool               // Whether this parameter is required
	Description string             // Human-readable description
}

// PatternSchema describes what patterns a pattern decorator accepts
type PatternSchema struct {
	AllowedPatterns     []string // Specific patterns allowed (e.g., ["main", "error", "finally"] for @try)
	RequiredPatterns    []string // Patterns that must be present (e.g., ["main"] for @try)
	AllowsWildcard      bool     // Whether "default" wildcard is allowed (e.g., true for @when)
	AllowsAnyIdentifier bool     // Whether any identifier is allowed (e.g., true for @when)
	Description         string   // Human-readable description of pattern rules
}

// ImportRequirement describes dependencies needed for code generation
type ImportRequirement struct {
	StandardLibrary []string          // Standard library imports (e.g., "time", "context", "sync")
	ThirdParty      []string          // Third-party imports (e.g., "github.com/pkg/errors")
	GoModules       map[string]string // Module dependencies for go.mod (module -> version)
}

// Decorator is a union interface for all decorator types
// Used for registry and common operations
type Decorator interface {
	Name() string
	Description() string
	ParameterSchema() []ParameterSchema

	// ImportRequirements returns the dependencies needed for code generation
	ImportRequirements() ImportRequirement
}

// ValueDecorator represents decorators that provide values for shell interpolation
// Examples: @var, @env - inline value substitution only
type ValueDecorator interface {
	Decorator

	// Expand returns a value that can be used in shell command composition
	// - GeneratorMode: Returns Go code expression that evaluates to the value
	// - InterpreterMode: Returns the actual runtime value
	// - PlanMode: Returns description for dry-run display
	Expand(ctx *execution.ExecutionContext, params []ast.NamedParameter) *execution.ExecutionResult
}

// ActionDecorator represents decorators that execute commands with structured output
// Examples: @cmd - can be standalone or chained with shell operators
type ActionDecorator interface {
	Decorator

	// Expand executes an action and returns structured result for chaining
	// - GeneratorMode: Returns Go code that produces CommandResult
	// - InterpreterMode: Executes and returns CommandResult
	// - PlanMode: Returns description for dry-run display
	Expand(ctx *execution.ExecutionContext, params []ast.NamedParameter) *execution.ExecutionResult
}

// BlockDecorator represents decorators that modify command execution behavior
// Examples: @watch, @stop, @parallel
type BlockDecorator interface {
	Decorator

	// Execute provides unified execution for all modes using the execution package
	Execute(ctx *execution.ExecutionContext, params []ast.NamedParameter, content []ast.CommandContent) *execution.ExecutionResult
}

// PatternDecorator represents decorators that handle pattern matching
// Examples: @when, @try
type PatternDecorator interface {
	Decorator

	// Execute provides unified execution for all modes using the execution package
	Execute(ctx *execution.ExecutionContext, params []ast.NamedParameter, patterns []ast.PatternBranch) *execution.ExecutionResult

	// PatternSchema defines what patterns this decorator accepts
	PatternSchema() PatternSchema
}


// DecoratorType represents the type of decorator
type DecoratorType int

const (
	ValueType DecoratorType = iota
	ActionType
	BlockType
	PatternType
)

// GetDecoratorType returns the type of a decorator
func GetDecoratorType(d Decorator) DecoratorType {
	switch d.(type) {
	case ValueDecorator:
		return ValueType
	case ActionDecorator:
		return ActionType
	case BlockDecorator:
		return BlockType
	case PatternDecorator:
		return PatternType
	default:
		panic("unknown decorator type")
	}
}
