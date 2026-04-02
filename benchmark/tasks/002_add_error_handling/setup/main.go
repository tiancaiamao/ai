package main

import (
	"errors"
	"fmt"
)

// Divide divides two numbers and returns an error if dividing by zero
func Divide(a, b float64) (float64, error) {
	if b == 0 {
		return 0, errors.New("division by zero")
	}
	return a / b, nil
}

// GetUserAge returns the age of a user from a map
// Returns an error if the user doesn't exist
func GetUserAge(users map[string]int, name string) (int, error) {
	age, ok := users[name]
	if !ok {
		return 0, fmt.Errorf("user %q not found", name)
	}
	return age, nil
}

func main() {
	result, err := Divide(10, 2)
	if err != nil {
		fmt.Println("Error:", err)
	} else {
		fmt.Println(result) // Works: 5
	}

	result, err = Divide(10, 0)
	if err != nil {
		fmt.Println("Error:", err) // Error: division by zero
	} else {
		fmt.Println(result)
	}

	users := map[string]int{"Alice": 30}

	age, err := GetUserAge(users, "Alice")
	if err != nil {
		fmt.Println("Error:", err)
	} else {
		fmt.Println(age) // Works: 30
	}

	age, err = GetUserAge(users, "Unknown")
	if err != nil {
		fmt.Println("Error:", err) // Error: user "Unknown" not found
	} else {
		fmt.Println(age)
	}
}
