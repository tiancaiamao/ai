package main

import (
	"fmt"
	"sync"
)

type Counter struct {
	value int
}

func (c *Counter) Increment() { c.value++ }
func (c *Counter) Value() int { return c.value }

func main() {
	var c Counter
	var wg sync.WaitGroup
	for i := 0; i < 1000; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 1000; j++ {
				c.Increment()
			}
		}()
	}
	wg.Wait()
	if c.Value() == 1000000 {
		fmt.Println("SUCCESS")
	} else {
		fmt.Println("FAIL:", c.Value())
	}
}