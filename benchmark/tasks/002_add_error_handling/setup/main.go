package main

import (
	"errors"
	"fmt"
)

// Divide divides two numbers
// Returns an error when dividing by zero
func Divide(a, b float64) (float64, error) {
	if b == 0 {
		return 0, errors.New("division by zero")
	}
	return a / b, nil
}

// GetUserAge returns the age of a user from a map
// Returns an error when the user doesn't exist
func GetUserAge(users map[string]int, name string) (int, error) {
	age, exists := users[name]
	if !exists {
		return 0, errors.New("user not found")
	}
	return age, nil
}

func main() {
	// Test Divide
	result, err := Divide(10, 2)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
	} else {
		fmt.Println(result) // Works: 5
	}

	result, err = Divide(10, 0)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
	} else {
		fmt.Println(result)
	}

	// Test GetUserAge
	users := map[string]int{"Alice": 30}
	age, err := GetUserAge(users, "Alice")
	if err != nil {
		fmt.Printf("Error: %v\n", err)
	} else {
		fmt.Println(age) // Works: 30
	}

	age, err = GetUserAge(users, "Unknown")
	if err != nil {
		fmt.Printf("Error: %v\n", err)
	} else {
		fmt.Println(age)
	}
}