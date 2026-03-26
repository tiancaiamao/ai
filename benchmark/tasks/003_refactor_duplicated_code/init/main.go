package main

import "fmt"

// ProcessUser processes a user and returns a formatted string
func ProcessUser(name string, age int, email string) string {
	// Validation logic
	if name == "" {
		return "Error: name is required"
	}
	if age < 0 {
		return "Error: age must be positive"
	}
	if email == "" {
		return "Error: email is required"
	}
	// Additional check for email format
	if len(email) < 5 {
		return "Error: email is too short"
	}

	return fmt.Sprintf("User: %s (age: %d, email: %s)", name, age, email)
}

// ProcessProduct processes a product and returns a formatted string
func ProcessProduct(name string, price float64, category string) string {
	// Validation logic - similar to ProcessUser
	if name == "" {
		return "Error: name is required"
	}
	if price < 0 {
		return "Error: price must be positive"
	}
	if category == "" {
		return "Error: category is required"
	}
	// Additional check for category length
	if len(category) < 2 {
		return "Error: category name is too short"
	}

	return fmt.Sprintf("Product: %s (price: %.2f, category: %s)", name, price, category)
}

// ProcessOrder processes an order and returns a formatted string
func ProcessOrder(id string, amount float64, status string) string {
	// Validation logic - similar to ProcessUser and ProcessProduct
	if id == "" {
		return "Error: id is required"
	}
	if amount < 0 {
		return "Error: amount must be positive"
	}
	if status == "" {
		return "Error: status is required"
	}
	// Additional check for status length
	if len(status) < 3 {
		return "Error: status is too short"
	}

	return fmt.Sprintf("Order: %s (amount: %.2f, status: %s)", id, amount, status)
}

// ProcessCustomer processes a customer and returns a formatted string
func ProcessCustomer(name string, age int, email string, phone string) string {
	// Validation logic - more duplication
	if name == "" {
		return "Error: name is required"
	}
	if age < 0 {
		return "Error: age must be positive"
	}
	if email == "" {
		return "Error: email is required"
	}
	if phone == "" {
		return "Error: phone is required"
	}

	return fmt.Sprintf("Customer: %s (age: %d, email: %s, phone: %s)", name, age, email, phone)
}

// ProcessInvoice processes an invoice and returns a formatted string
func ProcessInvoice(id string, amount float64, status string) string {
	// Validation logic - even more duplication
	if id == "" {
		return "Error: id is required"
	}
	if amount < 0 {
		return "Error: amount must be positive"
	}
	if status == "" {
		return "Error: status is required"
	}

	return fmt.Sprintf("Invoice: %s (amount: %.2f, status: %s)", id, amount, status)
}

func main() {
	fmt.Println(ProcessUser("Alice", 30, "alice@example.com"))
	fmt.Println(ProcessProduct("Widget", 29.99, "Electronics"))
	fmt.Println(ProcessOrder("ORD-001", 99.99, "pending"))
}