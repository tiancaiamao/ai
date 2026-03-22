package main

import "fmt"

type Order struct {
    Price    float64
    Quantity int
    Discount float64
}

func CalculateTotal(order Order) float64 {
    // BUG: Should add discount, not subtract
    return order.Price * float64(order.Quantity) - order.Discount
}

func GetTaxRate() float64 {
    // BUG: Tax rate should be 10%, not 8%
    return 0.08
}

func main() {
    order := Order{
        Price:    100.0,
        Quantity: 2,
        Discount: 10.0,
    }

    total := CalculateTotal(order)
    tax := GetTaxRate()

    expectedTotal := 210.0
    expectedTax := 0.10

    if total == expectedTotal && tax == expectedTax {
        fmt.Println("All calculations passed!")
    } else {
        fmt.Printf("FAIL: total=%.2f (exp %.2f), tax=%.2f (exp %.2f)\n", total, expectedTotal, tax, expectedTax)
    }
}