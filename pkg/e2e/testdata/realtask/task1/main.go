package main

import "fmt"

// SumRange returns the sum of numbers from 1 to n (inclusive).
func SumRange(n int) int {
	sum := 0
	for i := 1; i < n; i++ {
		sum += i
	}
	return sum
}

func main() {
	fmt.Println(SumRange(5))
}
