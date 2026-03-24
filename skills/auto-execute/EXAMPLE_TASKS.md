# Example Tasks for Auto-Execute

## Setup Phase
- [ ] TASK001: Create project directories
  **Acceptance Criteria**:
  - Create cmd/, internal/, pkg/ directories
  - Verify directories exist

- [ ] TASK002: Initialize Go module
  **Acceptance Criteria**:
  - Run `go mod init example.com/project`
  - Verify go.mod exists

## Implementation Phase
- [ ] TASK003: Create main function
  **Acceptance Criteria**:
  - Create cmd/app/main.go with main() function
  - Code compiles without errors

- [ ] TASK004: Implement helper function
  **Acceptance Criteria**:
  - Add helper function in internal/helper.go
  - Function returns "Hello, World!"
  - Has unit test passing

## Test Phase
- [ ] TASK005: Add unit tests
  **Acceptance Criteria**:
  - Create internal/helper_test.go
  - Test helper function
  - Run `go test ./...` - all pass

## Documentation Phase
- [ ] TASK006: Write README
  **Acceptance Criteria**:
  - Create README.md
  - Include project description
  - Include installation instructions
  - Include usage examples