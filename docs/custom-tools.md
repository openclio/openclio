# Custom Tools Guide

The agent executes actions via a set of native Go functions registered in the `tools` package. You can easily extend the agent by writing your own tools.

## The Tool Interface

All tools must implement `tools.Handler` (or be wrapped by one):

```go
type Handler func(ctx context.Context, args json.RawMessage) (string, error)
```

## Step 1: Write the Tool Logic

Create your function. It should accept JSON arguments and return a string (the result to pass back to the LLM) or an error.

```go
func myCustomTool(ctx context.Context, args json.RawMessage) (string, error) {
    var params struct {
        Target string `json:"target"`
    }
    if err := json.Unmarshal(args, &params); err != nil {
        return "", fmt.Errorf("invalid arguments: %w", err)
    }

    // Do something with params.Target
    result := fmt.Sprintf("Action performed on %s", params.Target)

    return result, nil
}
```

## Step 2: Define the Schema

The agent needs an OpenAI-compatible JSON schema to know how to use your tool.

```go
const myCustomToolDef = `
{
    "name": "my_custom_tool",
    "description": "Performs a custom action on a target.",
    "parameters": {
        "type": "object",
        "properties": {
            "target": {
                "type": "string",
                "description": "The target to act upon."
            }
        },
        "required": ["target"]
    }
}
`
```

## Step 3: Register the Tool

In `internal/tools/registry.go` (or wherever you manage the registry instance), register the definition and the handler.

```go
registry.Register(myCustomToolDef, myCustomTool)
```

## Security Considerations

- **Isolate execution:** If your tool executes code or shell commands, make sure it applies timeouts and directory sandboxing.
- **Scrub secrets:** The core `agent.Run` loop generally scrubs responses, but ensure your tool itself doesn't unnecessarily leak API keys from environment variables into its return string.
