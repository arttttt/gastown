# Design: Doctor Check for .opencode/commands/ Provisioning

## Overview
Add Doctor check to verify `.opencode/commands/` is provisioned at town level. Mirrors existing `.claude/commands/` check.

## Implementation Plan

### 1. Templates Module (`internal/templates/templates.go`)
Add function:
```go
func MissingCommandsOpenCode(workspacePath string) ([]string, error)
```
- Similar to `MissingCommands()` 
- Reads from `commands-opencode/` embedded directory
- Checks against `.opencode/commands/` directory

### 2. Doctor Check (`internal/doctor/commands_opencode_check.go`)
Create new file with:
- `CommandsOpenCodeCheck` struct (mirrors `CommandsCheck`)
- `NewCommandsOpenCodeCheck()` constructor
- `Run()` method to check if commands exist
- `Fix()` method to provision missing commands
- Uses `templates.MissingCommandsOpenCode()` and `templates.ProvisionCommandsOpenCode()`

### 3. Doctor Registration (`internal/cmd/doctor.go`)
Register check:
```go
d.Register(doctor.NewCommandsOpenCodeCheck())
```
Place after `NewCommandsCheck()` registration.

## Dependencies
- `templates.MissingCommandsOpenCode()` (new)
- `templates.ProvisionCommandsOpenCode()` (exists)
