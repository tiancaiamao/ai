package main

import (
	"encoding/json"
	"fmt"
)

// parseJSON parses a JSON string and returns an error for invalid JSON
func parseJSON(s string) (map[string]interface{}, error) {
	var result map[string]interface{}
	err := json.Unmarshal([]byte(s), &result)
	if err != nil {
		return nil, err
	}
	return result, nil
}

// toJSON converts a map to JSON string and returns an error if marshaling fails
func toJSON(m map[string]interface{}) (string, error) {
	bytes, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	return string(bytes), nil
}

func main() {
	// Valid JSON
	valid := `{"name": "Alice", "age": 30}`
	result, err := parseJSON(valid)
	if err != nil {
		fmt.Printf("Error parsing valid JSON: %v\n", err)
	} else {
		fmt.Printf("Name: %s, Age: %v\n", result["name"], result["age"])
	}

	// Invalid JSON - should return error
	invalid := `{"name": "Bob", "age": }`
	result2, err := parseJSON(invalid)
	if err != nil {
		fmt.Printf("Error parsing invalid JSON: %v\n", err)
	} else {
		fmt.Printf("Result2: %v\n", result2)
	}
}