package main

import "fmt"

// SumRange returns the sum of numbers from 1 to n
// BUG: This has an off-by-one error
func SumRange(n int) int {
	sum := 0
	for i := 1; i < n; i++ { // BUG: should be i <= n
		sum += i
	}
	return sum
}

func main() {
	fmt.Println(SumRange(5)) // Should print 15, but prints 10
}
