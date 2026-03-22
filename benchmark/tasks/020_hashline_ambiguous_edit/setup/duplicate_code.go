package main

import "fmt"

// ProcessorA handles items of type A
type ProcessorA struct{}

func (p *ProcessorA) ProcessItem(items []int) int {
	sum := 0
	for i := 0; i <= len(items); i++ {
		if i < len(items) {
			sum += items[i]
		}
	}
	return sum
}

// ProcessorB handles items of type B
type ProcessorB struct{}

func (p *ProcessorB) ProcessItem(items []int) int {
	sum := 0
	for i := 0; i <= len(items); i++ {
		if i < len(items) {
			sum += items[i]
		}
	}
	return sum
}

// ProcessorC handles items of type C
type ProcessorC struct{}

func (p *ProcessorC) ProcessItem(items []int) int {
	sum := 0
	for i := 0; i <= len(items); i++ {
		if i < len(items) {
			sum += items[i]
		}
	}
	return sum
}

func main() {
	items := []int{1, 2, 3, 4, 5}

	a := &ProcessorA{}
	b := &ProcessorB{}
	c := &ProcessorC{}

	resultA := a.ProcessItem(items)
	resultB := b.ProcessItem(items)
	resultC := c.ProcessItem(items)

	// Expected results after fix:
	// A should process 4 items (skip last) → 1+2+3+4 = 10
	// B should process all 5 items → 1+2+3+4+5 = 15
	// C should process 4 items (skip last) → 1+2+3+4 = 10

	if resultA == 10 && resultB == 15 && resultC == 10 {
		fmt.Println("All processors passed!")
	} else {
		fmt.Printf("FAIL: A=%d (exp 10), B=%d (exp 15), C=%d (exp 10)\n", resultA, resultB, resultC)
	}
}