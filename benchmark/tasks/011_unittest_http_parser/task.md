# Task: Write Unit Tests for HTTP File Parser

## Description

The code in the `setup/httpfile/` folder parses the JetBrains .http file format. Write comprehensive unit tests for the parser.

## Context

The parser is a state machine that handles HTTP requests with:
- Headers
- Bodies
- Comments
- Request separators (`###`)

An `example.http` file is provided as reference in the setup directory.

## Requirements

1. Use Go's testing framework
2. Follow Go's testdata directory convention
3. Test edge cases:
   - Malformed input
   - Empty files
   - Missing headers
   - Multiple requests in one file

## Example .http Format

```http
### Get Users
GET https://api.example.com/users
Authorization: Bearer token123

### Create User
POST https://api.example.com/users
Content-Type: application/json

{
  "name": "John",
  "email": "john@example.com"
}
```

## Success Criteria

1. `go test ./...` passes
2. Tests cover the main parsing functionality
3. At least one test file exists in `setup/`
