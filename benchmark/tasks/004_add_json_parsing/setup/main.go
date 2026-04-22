package main

import (
	"encoding/json"
	"fmt"
)

// parseJSON parses a JSON string
// BUG: No error handling for invalid JSON
func parseJSON(s string) map[string]interface{} {
	var result map[string]interface{}
	json.Unmarshal([]byte(s), &result)
	return result
}

// toJSON converts a map to JSON string
func toJSON(m map[string]interface{}) string {
	bytes, _ := json.Marshal(m)
	return string(bytes)
}

func main() {
	// Valid JSON
	valid := `{"name": "Alice", "age": 30}`
	result := parseJSON(valid)
	fmt.Printf("Name: %s, Age: %v\n", result["name"], result["age"])

	// Invalid JSON - should return error
	invalid := `{"name": "Bob", "age": }`
	result2 := parseJSON(invalid)
	fmt.Printf("Result2: %v\n", result2)
}
