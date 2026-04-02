package main

import (
	"encoding/json"
	"fmt"
)

// parseJSON parses a JSON string and returns an error for invalid JSON
func parseJSON(s string) (map[string]interface{}, error) {
	if !json.Valid([]byte(s)) {
		return nil, fmt.Errorf("invalid JSON: %s", s)
	}
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
		fmt.Printf("Error: %v\n", err)
	} else {
		fmt.Printf("Name: %s, Age: %v\n", result["name"], result["age"])
	}

	// Invalid JSON - should return error
	invalid := `{"name": "Bob", "age": }`
	result2, err2 := parseJSON(invalid)
	if err2 != nil {
		fmt.Printf("Error parsing invalid JSON: %v\n", err2)
	} else {
		fmt.Printf("Result2: %v\n", result2)
	}

	// Test toJSON
	m := map[string]interface{}{"name": "Charlie", "age": 25}
	jsonStr, err := toJSON(m)
	if err != nil {
		fmt.Printf("Error marshaling: %v\n", err)
	} else {
		fmt.Printf("JSON: %s\n", jsonStr)
	}
}
