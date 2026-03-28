package main

import "fmt"

func check(ok bool, msg string) string {
	if !ok { return "Error: " + msg }
	return ""
}

func ProcessUser(name string, age int, email string) string {
	if e := check(name != "", "name is required"); e != "" { return e }
	if e := check(age >= 0, "age must be positive"); e != "" { return e }
	if e := check(email != "", "email is required"); e != "" { return e }
	if e := check(len(email) >= 5, "email is too short"); e != "" { return e }
	return fmt.Sprintf("User: %s (age: %d, email: %s)", name, age, email)
}

func ProcessProduct(name string, price float64, category string) string {
	if e := check(name != "", "name is required"); e != "" { return e }
	if e := check(price >= 0, "price must be positive"); e != "" { return e }
	if e := check(category != "", "category is required"); e != "" { return e }
	if e := check(len(category) >= 2, "category name is too short"); e != "" { return e }
	return fmt.Sprintf("Product: %s (price: %.2f, category: %s)", name, price, category)
}

func ProcessOrder(id string, amount float64, status string) string {
	if e := check(id != "", "id is required"); e != "" { return e }
	if e := check(amount >= 0, "amount must be positive"); e != "" { return e }
	if e := check(status != "", "status is required"); e != "" { return e }
	if e := check(len(status) >= 3, "status is too short"); e != "" { return e }
	return fmt.Sprintf("Order: %s (amount: %.2f, status: %s)", id, amount, status)
}

func ProcessCustomer(name string, age int, email string, phone string) string {
	if e := check(name != "", "name is required"); e != "" { return e }
	if e := check(age >= 0, "age must be positive"); e != "" { return e }
	if e := check(email != "", "email is required"); e != "" { return e }
	if e := check(phone != "", "phone is required"); e != "" { return e }
	return fmt.Sprintf("Customer: %s (age: %d, email: %s, phone: %s)", name, age, email, phone)
}

func ProcessInvoice(id string, amount float64, status string) string {
	if e := check(id != "", "id is required"); e != "" { return e }
	if e := check(amount >= 0, "amount must be positive"); e != "" { return e }
	if e := check(status != "", "status is required"); e != "" { return e }
	return fmt.Sprintf("Invoice: %s (amount: %.2f, status: %s)", id, amount, status)
}

func main() {
	fmt.Println(ProcessUser("Alice", 30, "alice@example.com"))
	fmt.Println(ProcessProduct("Widget", 29.99, "Electronics"))
	fmt.Println(ProcessOrder("ORD-001", 99.99, "pending"))
}