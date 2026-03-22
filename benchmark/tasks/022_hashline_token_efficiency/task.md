# Task: Multi-File Codebase with Large Files

## Description
This task involves fixing bugs across multiple files in a codebase. This test compares token efficiency between hashline and non-hashline modes when reading and editing files.

## Challenge
- Read and understand multiple files
- Fix bugs in specific locations
- Track token usage for file reads
- Compare hashline vs non-hashline token consumption

## Expected Behavior
- Fix the bug in `models/user.go` (change `Age string` to `Age int`)
- Fix the bug in `services/user_service.go` (change `user.UserAge` to `user.Age`)
- Fix the bug in `main.go` (change `user.UserAge` to `user.Age`)
- Run `go run main.go models/user.go services/user_service.go` - should print "All validations passed!"

## Steps
1. Read all three files: `main.go`, `models/user.go`, `services/user_service.go`
2. Identify and fix all three bugs
3. Verify with `go run main.go models/user.go services/user_service.go`

## Note
This task is primarily for measuring token usage differences between hashline and non-hashline modes. The actual code changes are straightforward.