package decorators

import (
	"fmt"
	"strings"

	"github.com/aledsdavies/devcmd/core/ast"
	"github.com/aledsdavies/devcmd/core/plan"
	"github.com/aledsdavies/devcmd/runtime/decorators"
	"github.com/aledsdavies/devcmd/runtime/execution"
)

// TryDecorator implements the @try decorator for error handling with pattern matching
type TryDecorator struct{}


// Name returns the decorator name
func (t *TryDecorator) Name() string {
	return "try"
}

// Description returns a human-readable description
func (t *TryDecorator) Description() string {
	return "Execute commands with try-catch-finally semantics (main required, catch/finally optional but at least one required)"
}

// ParameterSchema returns the expected parameters for this decorator
func (t *TryDecorator) ParameterSchema() []decorators.ParameterSchema {
	return []decorators.ParameterSchema{} // @try takes no parameters
}

// decorators.PatternSchema defines what patterns @try accepts
func (t *TryDecorator) PatternSchema() decorators.PatternSchema {
	return decorators.PatternSchema{
		AllowedPatterns:     []string{"main", "catch", "finally"},
		RequiredPatterns:    []string{"main"},
		AllowsWildcard:      false, // No "default" wildcard for @try
		AllowsAnyIdentifier: false, // Only specific patterns allowed
		Description:         "Requires 'main', optionally accepts 'catch' and 'finally'",
	}
}

// Validate checks if the decorator usage is correct during parsing

// ExecuteInterpreter executes try-catch-finally in interpreter mode
func (t *TryDecorator) ExecuteInterpreter(ctx execution.InterpreterContext, params []ast.NamedParameter, patterns []ast.PatternBranch) *execution.ExecutionResult {
	mainBranch, catchBranch, finallyBranch, err := t.validateAndExtractPatterns(params, patterns)
	if err != nil {
		return &execution.ExecutionResult{
			Data:  nil,
			Error: err,
		}
	}

	return t.executeInterpreterImpl(ctx, mainBranch, catchBranch, finallyBranch)
}

// ExecuteGenerator generates Go code for try-catch-finally logic
func (t *TryDecorator) ExecuteGenerator(ctx execution.GeneratorContext, params []ast.NamedParameter, patterns []ast.PatternBranch) *execution.ExecutionResult {
	mainBranch, catchBranch, finallyBranch, err := t.validateAndExtractPatterns(params, patterns)
	if err != nil {
		return &execution.ExecutionResult{
			Data:  "",
			Error: err,
		}
	}

	return t.executeGeneratorImpl(ctx, mainBranch, catchBranch, finallyBranch)
}

// ExecutePlan creates a plan element for dry-run mode
func (t *TryDecorator) ExecutePlan(ctx execution.PlanContext, params []ast.NamedParameter, patterns []ast.PatternBranch) *execution.ExecutionResult {
	mainBranch, catchBranch, finallyBranch, err := t.validateAndExtractPatterns(params, patterns)
	if err != nil {
		return &execution.ExecutionResult{
			Data:  nil,
			Error: err,
		}
	}

	return t.executePlanImpl(ctx, mainBranch, catchBranch, finallyBranch)
}

// validateAndExtractPatterns validates parameters and extracts pattern branches
func (t *TryDecorator) validateAndExtractPatterns(params []ast.NamedParameter, patterns []ast.PatternBranch) (*ast.PatternBranch, *ast.PatternBranch, *ast.PatternBranch, error) {
	// Validate parameters first
	if len(params) > 0 {
		return nil, nil, nil, fmt.Errorf("try decorator takes no parameters, got %d", len(params))
	}

	// Find pattern branches
	var mainBranch, catchBranch, finallyBranch *ast.PatternBranch

	for i := range patterns {
		pattern := &patterns[i]
		patternStr := t.patternToString(pattern.Pattern)

		switch patternStr {
		case "main":
			mainBranch = pattern
		case "catch":
			catchBranch = pattern
		case "finally":
			finallyBranch = pattern
		default:
			return nil, nil, nil, fmt.Errorf("@try only supports 'main', 'catch', and 'finally' patterns, got '%s'", patternStr)
		}
	}

	// Validate required patterns
	if mainBranch == nil {
		return nil, nil, nil, fmt.Errorf("@try requires a 'main' pattern")
	}
	if catchBranch == nil && finallyBranch == nil {
		return nil, nil, nil, fmt.Errorf("@try requires at least one of 'catch' or 'finally' patterns")
	}

	return mainBranch, catchBranch, finallyBranch, nil
}

// executeInterpreterImpl executes try-catch-finally in interpreter mode using utilities
func (t *TryDecorator) executeInterpreterImpl(ctx execution.InterpreterContext, mainBranch, catchBranch, finallyBranch *ast.PatternBranch) *execution.ExecutionResult {
	var mainErr, catchErr, finallyErr error

	// Create CommandExecutor for executing commands
	commandExecutor := decorators.NewCommandExecutor()
	defer commandExecutor.Cleanup()

	// Execute main block
	mainErr = commandExecutor.ExecuteCommandsWithInterpreter(ctx.Child(), mainBranch.Commands)

	// Execute catch block if main failed and catch pattern exists
	if mainErr != nil && catchBranch != nil {
		// Catch block executes in isolated context
		catchErr = commandExecutor.ExecuteCommandsWithInterpreter(ctx.Child(), catchBranch.Commands)
	}

	// Always execute finally block if it exists, regardless of main/catch success
	if finallyBranch != nil {
		// Finally block executes in isolated context
		finallyErr = commandExecutor.ExecuteCommandsWithInterpreter(ctx.Child(), finallyBranch.Commands)
	}

	// Return the most significant error: main error takes precedence
	if mainErr != nil {
		return &execution.ExecutionResult{
			Data:  nil,
			Error: fmt.Errorf("main block failed: %w", mainErr),
		}
	}
	if catchErr != nil {
		return &execution.ExecutionResult{
			Data:  nil,
			Error: fmt.Errorf("catch block failed: %w", catchErr),
		}
	}
	if finallyErr != nil {
		return &execution.ExecutionResult{
			Data:  nil,
			Error: fmt.Errorf("finally block failed: %w", finallyErr),
		}
	}

	return &execution.ExecutionResult{
		Data:  nil,
		Error: nil,
	}
}

// executeGeneratorImpl generates Go code for try-catch-finally logic using new utilities
func (t *TryDecorator) executeGeneratorImpl(ctx execution.GeneratorContext, mainBranch, catchBranch, finallyBranch *ast.PatternBranch) *execution.ExecutionResult {
	// Convert main commands to operation
	mainOperations, err := decorators.ConvertCommandsToOperations(ctx, mainBranch.Commands)
	if err != nil {
		return &execution.ExecutionResult{
			Data:  "",
			Error: fmt.Errorf("failed to convert main commands: %w", err),
		}
	}

	// Combine main operations into single operation
	mainOp, err := t.combineOperations(mainOperations)
	if err != nil {
		return &execution.ExecutionResult{
			Data:  "",
			Error: fmt.Errorf("failed to combine main operations: %w", err),
		}
	}

	// Convert catch commands to operation if they exist
	var catchOp *decorators.Operation
	if catchBranch != nil {
		catchOperations, err := decorators.ConvertCommandsToOperations(ctx, catchBranch.Commands)
		if err != nil {
			return &execution.ExecutionResult{
				Data:  "",
				Error: fmt.Errorf("failed to convert catch commands: %w", err),
			}
		}
		combinedCatch, err := t.combineOperations(catchOperations)
		if err != nil {
			return &execution.ExecutionResult{
				Data:  "",
				Error: fmt.Errorf("failed to combine catch operations: %w", err),
			}
		}
		catchOp = &combinedCatch
	}

	// Convert finally commands to operation if they exist
	var finallyOp *decorators.Operation
	if finallyBranch != nil {
		finallyOperations, err := decorators.ConvertCommandsToOperations(ctx, finallyBranch.Commands)
		if err != nil {
			return &execution.ExecutionResult{
				Data:  "",
				Error: fmt.Errorf("failed to convert finally commands: %w", err),
			}
		}
		combinedFinally, err := t.combineOperations(finallyOperations)
		if err != nil {
			return &execution.ExecutionResult{
				Data:  "",
				Error: fmt.Errorf("failed to combine finally operations: %w", err),
			}
		}
		finallyOp = &combinedFinally
	}

	// Use TemplateBuilder to create try-catch-finally pattern
	builder := decorators.NewTemplateBuilder()
	builder.WithTryCatchFinally(mainOp, catchOp, finallyOp)

	// Build the template
	generatedCode, err := builder.BuildTemplate()
	if err != nil {
		return &execution.ExecutionResult{
			Data:  "",
			Error: fmt.Errorf("failed to build try-catch-finally template: %w", err),
		}
	}

	return &execution.ExecutionResult{
		Data:  generatedCode,
		Error: nil,
	}
}

// executePlanImpl creates a plan element for dry-run mode showing try-catch-finally structure
func (t *TryDecorator) executePlanImpl(ctx execution.PlanContext, mainBranch, catchBranch, finallyBranch *ast.PatternBranch) *execution.ExecutionResult {
	// Build description
	description := "Error handling with "
	var parts []string
	if mainBranch != nil {
		parts = append(parts, fmt.Sprintf("main (%d commands)", len(mainBranch.Commands)))
	}
	if catchBranch != nil {
		parts = append(parts, fmt.Sprintf("catch (%d commands)", len(catchBranch.Commands)))
	}
	if finallyBranch != nil {
		parts = append(parts, fmt.Sprintf("finally (%d commands)", len(finallyBranch.Commands)))
	}
	description += strings.Join(parts, ", ")

	// Create the main decorator element
	element := plan.Decorator("try").
		WithType("pattern").
		WithDescription(description)

	// Add main commands directly as children (always executed first)
	if mainBranch != nil {
		for _, cmd := range mainBranch.Commands {
			switch c := cmd.(type) {
			case *ast.ShellContent:
				result := ctx.GenerateShellPlan(c)
				if result.Error != nil {
					return &execution.ExecutionResult{
						Data:  nil,
						Error: fmt.Errorf("failed to create plan for main command: %w", result.Error),
					}
				}
				if childPlan, ok := result.Data.(*plan.ExecutionStep); ok {
					// Convert ExecutionStep to a Command element for the plan
					cmdElement := plan.Command(childPlan.Command).WithDescription(childPlan.Description)
					element = element.AddChild(cmdElement)
				}
			case *ast.BlockDecorator:
				// For nested decorators, create a plan element
				childElement := plan.Command(fmt.Sprintf("@%s{...}", c.Name)).WithDescription("Nested decorator")
				element = element.AddChild(childElement)
			default:
				// Unknown command type
				childElement := plan.Command(fmt.Sprintf("Unknown command: %T", cmd)).WithDescription("Unsupported command")
				element = element.AddChild(childElement)
			}
		}
	}

	// Add catch block as a conditional child (executed only on error)
	if catchBranch != nil {
		// Create a conditional element for the catch block
		catchElement := plan.Decorator("[on error]").WithType("conditional").WithDescription("Executed only if main block fails")
		
		// Add catch commands as children of the conditional element
		for _, cmd := range catchBranch.Commands {
			switch c := cmd.(type) {
			case *ast.ShellContent:
				result := ctx.GenerateShellPlan(c)
				if result.Error != nil {
					return &execution.ExecutionResult{
						Data:  nil,
						Error: fmt.Errorf("failed to create plan for catch command: %w", result.Error),
					}
				}
				if childPlan, ok := result.Data.(*plan.ExecutionStep); ok {
					// Convert ExecutionStep to a Command element for the plan
					cmdElement := plan.Command(childPlan.Command).WithDescription(childPlan.Description)
					catchElement = catchElement.AddChild(cmdElement)
				}
			case *ast.BlockDecorator:
				// For nested decorators in catch
				childElement := plan.Command(fmt.Sprintf("@%s{...}", c.Name)).WithDescription("Nested decorator in catch")
				catchElement = catchElement.AddChild(childElement)
			default:
				// Unknown command type in catch
				childElement := plan.Command(fmt.Sprintf("Unknown command: %T", cmd)).WithDescription("Unsupported command in catch")
				catchElement = catchElement.AddChild(childElement)
			}
		}
		
		// Add the catch element to the main try element
		element = element.AddChild(catchElement)
	}

	// Add finally block as an always-executed child
	if finallyBranch != nil {
		// Create an element for the finally block
		finallyElement := plan.Decorator("[always]").WithType("block").WithDescription("Always executed regardless of success/failure")
		
		// Add finally commands as children of the finally element
		for _, cmd := range finallyBranch.Commands {
			switch c := cmd.(type) {
			case *ast.ShellContent:
				result := ctx.GenerateShellPlan(c)
				if result.Error != nil {
					return &execution.ExecutionResult{
						Data:  nil,
						Error: fmt.Errorf("failed to create plan for finally command: %w", result.Error),
					}
				}
				if childPlan, ok := result.Data.(*plan.ExecutionStep); ok {
					// Convert ExecutionStep to a Command element for the plan
					cmdElement := plan.Command(childPlan.Command).WithDescription(childPlan.Description)
					finallyElement = finallyElement.AddChild(cmdElement)
				}
			case *ast.BlockDecorator:
				// For nested decorators in finally
				childElement := plan.Command(fmt.Sprintf("@%s{...}", c.Name)).WithDescription("Nested decorator in finally")
				finallyElement = finallyElement.AddChild(childElement)
			default:
				// Unknown command type in finally
				childElement := plan.Command(fmt.Sprintf("Unknown command: %T", cmd)).WithDescription("Unsupported command in finally")
				finallyElement = finallyElement.AddChild(childElement)
			}
		}
		
		// Add the finally element to the main try element
		element = element.AddChild(finallyElement)
	}

	return &execution.ExecutionResult{
		Data:  element,
		Error: nil,
	}
}

// combineOperations combines multiple operations into a single sequential operation
func (t *TryDecorator) combineOperations(operations []decorators.Operation) (decorators.Operation, error) {
	if len(operations) == 0 {
		return decorators.Operation{Code: "// No operations"}, nil
	}
	
	if len(operations) == 1 {
		return operations[0], nil
	}

	// Use TemplateBuilder to create sequential execution for multiple operations
	builder := decorators.NewTemplateBuilder()
	builder.WithSequentialExecution(operations, true) // Stop on error
	
	code, err := builder.BuildTemplate()
	if err != nil {
		return decorators.Operation{}, fmt.Errorf("failed to combine operations: %w", err)
	}
	
	return decorators.Operation{Code: code}, nil
}

// patternToString converts a pattern to its string representation
func (t *TryDecorator) patternToString(pattern ast.Pattern) string {
	switch p := pattern.(type) {
	case *ast.IdentifierPattern:
		return p.Name
	default:
		return "unknown"
	}
}

// ImportRequirements returns the dependencies needed for code generation
func (t *TryDecorator) ImportRequirements() decorators.ImportRequirement {
	return decorators.ImportRequirement{
		StandardLibrary: []string{"fmt", "os"}, // Required by TryCatchFinallyPattern
		ThirdParty:      []string{},
		GoModules:       map[string]string{},
	}
}

// init registers the try decorator
func init() {
	decorators.RegisterPattern(&TryDecorator{})
}
