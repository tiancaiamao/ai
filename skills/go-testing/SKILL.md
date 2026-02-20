---
name: go-testing
description: Write comprehensive unit tests for Go code using testing package and best practices
---

When writing Go tests, follow these guidelines:

## Test Structure

1. Use table-driven tests for multiple scenarios
2. Follow AAA pattern: Arrange, Act, Assert
3. Use descriptive test names that explain what is being tested
4. Group related tests in subtests using t.Run()

## Test File Organization

```go
// Package being tested
package mypackage

import "testing"

func TestFunctionName(t *testing.T) {
    tests := []struct {
        name     string
        input    string
        expected string
        wantErr  bool
    }{
        {"basic case", "input", "output", false},
        {"empty input", "", "", true},
        {"special chars", "!@#$", "", false},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            result, err := FunctionName(tt.input)
            if (err != nil) != tt.wantErr {
                t.Errorf("FunctionName() error = %v, wantErr %v", err, tt.wantErr)
                return
            }
            if result != tt.expected {
                t.Errorf("FunctionName() = %v, want %v", result, tt.expected)
            }
        })
    }
}
```

## Best Practices

1. **Test Public APIs Only**: Don't test unexported functions directly
2. **Use TestHelpers**: Extract common test setup into helper functions
3. **Mock External Dependencies**: Use interfaces for external dependencies
4. **Test Both Success and Failure Cases**: Ensure error handling works
5. **Keep Tests Fast**: Use t.Parallel() for independent tests
6. **Use Test Coverage**: Aim for >80% coverage

## Example with Mocks

```go
type MockRepository struct {
    users map[string]User
    err   error
}

func (m *MockRepository) GetUser(id string) (User, error) {
    if m.err != nil {
        return User{}, m.err
    }
    return m.users[id], nil
}

func TestUserService(t *testing.T) {
    mock := &MockRepository{
        users: map[string]User{
            "1": {ID: "1", Name: "Test"},
        },
    }
    
    service := NewUserService(mock)
    
    user, err := service.GetUser("1")
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
    if user.Name != "Test" {
        t.Errorf("expected name Test, got %s", user.Name)
    }
}
```

## Benchmarks

```go
func BenchmarkFunction(b *testing.B) {
    for i := 0; i < b.N; i++ {
        FunctionName("test input")
    }
}
```

Run with: `go test -bench=. -benchmem`
