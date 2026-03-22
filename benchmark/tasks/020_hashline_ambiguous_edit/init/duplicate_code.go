package main

import "fmt"

// ProcessorA handles items of type A
type ProcessorA struct{}

func (p *ProcessorA) ProcessItem(items []int) int {
	sum := 0
	for i := 0; i < len(items); i++ { // BUG: off-by-one, but leave this one unchanged
		sum += items[i]
	}
	return sum
}

// ProcessorB handles items of type B
type ProcessorB struct{}

func (p *ProcessorB) ProcessItem(items []int) int {
	sum := 0
	for i := 0; i < len(items); i++ { // BUG: off-by-one - FIX THIS ONE ONLY
		sum += items[i]
	}
	return sum
}

// ProcessorC handles items of type C
type ProcessorC struct{}

func (p *ProcessorC) ProcessItem(items []int) int {
	sum := 0
	for i := 0; i < len(items); i++ { // BUG: off-by-one, but leave this one unchanged
		sum += items[i]
	}
	return sum
}

func main() {
	items := []int{1, 2, 3, 4, 5}

	// Test ProcessorA - should return 10 (1+2+3+4)
	a := &ProcessorA{}
	resultA := a.ProcessItem(items)

	// Test ProcessorB - should return 15 after fix
	b := &ProcessorB{}
	resultB := b.ProcessItem(items)

	// Test ProcessorC - should return 10 (1+2+3+4)
	c := &ProcessorC{}
	resultC := c.ProcessItem(items)

	// Verification: Only ProcessorB should be fixed
	// After fix: resultA=10, resultB=15, resultC=10
	expectedA := 10
	expectedB := 15
	expectedC := 10

	if resultA == expectedA && resultB == expectedB && resultC == expectedC {
		fmt.Println("All processors passed!")
	} else {
		fmt.Printf("FAIL: A=%d (exp %d), B=%d (exp %d), C=%d (exp %d)\n",
			resultA, expectedA, resultB, expectedB, resultC, expectedC)
	}
}