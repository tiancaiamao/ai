package main

import (
	"fmt"
)

// Divide divides two numbers
// BUG: No error handling for division by zero
func Divide(a, b float64) float64 {
	return a / b // Will return Inf when b is 0
}

// GetUserAge returns the age of a user from a map
// BUG: No error handling for missing key
func GetUserAge(users map[string]int, name string) int {
	return users[name] // Returns 0 if key doesn't exist
}

func main() {
	fmt.Println(Divide(10, 2)) // Works: 5
	fmt.Println(Divide(10, 0)) // Bug: should return error

	users := map[string]int{"Alice": 30}
	fmt.Println(GetUserAge(users, "Alice"))   // Works: 30
	fmt.Println(GetUserAge(users, "Unknown")) // Bug: should return error
}
