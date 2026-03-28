package main

import (
	"fmt"
	"sync"
)

// Counter is a simple counter that should be thread-safe
type Counter struct {
	mu    sync.Mutex
	value int
}

// Increment increases the counter by 1
func (c *Counter) Increment() {
	c.mu.Lock()
	c.value++
	c.mu.Unlock()
}

// Value returns the current counter value
func (c *Counter) Value() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.value
}

func main() {
	counter := &Counter{}
	var wg sync.WaitGroup

	// Start 1000 goroutines, each incrementing 1000 times
	for i := 0; i < 1000; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 1000; j++ {
				counter.Increment()
			}
		}()
	}

	wg.Wait()

	// Expected: 1000000
	// Actual: varies (race condition)
	fmt.Printf("Counter value: %d\n", counter.Value())
	fmt.Printf("Expected: 1000000\n")

	if counter.Value() == 1000000 {
		fmt.Println("SUCCESS: Counter is thread-safe!")
	} else {
		fmt.Println("FAIL: Race condition detected!")
	}
}
