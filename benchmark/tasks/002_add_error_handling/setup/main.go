package main

import (
	"errors"
	"fmt"
)

// Divide divides two numbers
func Divide(a, b float64) (float64, error) {
	if b == 0 {
		return 0, errors.New("division by zero")
	}
	return a / b, nil
}

// GetUserAge returns the age of a user from a map
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
		fmt.Println(result)
	}

	result, err = Divide(10, 0)
	if err != nil {
		fmt.Println("Error:", err)
	} else {
		fmt.Println(result)
	}

	users := map[string]int{"Alice": 30}

	age, err := GetUserAge(users, "Alice")
	if err != nil {
		fmt.Println("Error:", err)
	} else {
		fmt.Println(age)
	}

	age, err = GetUserAge(users, "Unknown")
	if err != nil {
		fmt.Println("Error:", err)
	} else {
		fmt.Println(age)
	}
}