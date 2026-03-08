package main

import "fmt"

// ValidationResult holds the result of a validation
type ValidationResult struct {
	Valid   bool
	Message string
}

// ValidateString checks if a string is non-empty
func ValidateString(value, fieldName string) ValidationResult {
	if value == "" {
		return ValidationResult{Valid: false, Message: fmt.Sprintf("Error: %s is required", fieldName)}
	}
	return ValidationResult{Valid: true, Message: ""}
}

// ValidatePositive checks if a numeric value is non-negative
// Using generics to support both int and float64
func ValidatePositive[T int | float64](value T, fieldName string) ValidationResult {
	var zero T
	if value < zero {
		return ValidationResult{Valid: false, Message: fmt.Sprintf("Error: %s must be positive", fieldName)}
	}
	return ValidationResult{Valid: true, Message: ""}
}

// ValidateFields takes variadic ValidationResults and returns the first error message if any validation fails
// This eliminates the duplicated validation pattern across ProcessUser, ProcessProduct, and ProcessOrder
func ValidateFields(results ...ValidationResult) string {
	for _, result := range results {
		if !result.Valid {
			return result.Message
		}
	}
	return ""
}

// ProcessUser processes a user and returns a formatted string
func ProcessUser(name string, age int, email string) string {
	// Validation using helper functions
	if err := ValidateFields(ValidateString(name, "name"), ValidatePositive(age, "age"), ValidateString(email, "email")); err != "" {
		return err
	}

	return fmt.Sprintf("User: %s (age: %d, email: %s)", name, age, email)
}

// ProcessProduct processes a product and returns a formatted string
func ProcessProduct(name string, price float64, category string) string {
	// Validation using helper functions
	if err := ValidateFields(ValidateString(name, "name"), ValidatePositive(price, "price"), ValidateString(category, "category")); err != "" {
		return err
	}

	return fmt.Sprintf("Product: %s (price: %.2f, category: %s)", name, price, category)
}

// ProcessOrder processes an order and returns a formatted string
func ProcessOrder(id string, amount float64, status string) string {
	// Validation using helper functions
	if err := ValidateFields(ValidateString(id, "id"), ValidatePositive(amount, "amount"), ValidateString(status, "status")); err != "" {
		return err
	}

	return fmt.Sprintf("Order: %s (amount: %.2f, status: %s)", id, amount, status)
}

func main() {
	fmt.Println(ProcessUser("Alice", 30, "alice@example.com"))
	fmt.Println(ProcessProduct("Widget", 29.99, "Electronics"))
	fmt.Println(ProcessOrder("ORD-001", 99.99, "pending"))
}