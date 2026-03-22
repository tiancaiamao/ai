package main

import (
    "fmt"
    "os"

    "example.com/mysite/models"
    "example.com/mysite/services"
)

func main() {
    // Test user validation
    user := &models.User{
        ID:       1,
        Name:     "John Doe",
        Email:    "john@example.com",
        // BUG: Should use Age (int), not UserAge (string)
        Age:      "25",
        Address:  "123 Main St",
        City:     "San Francisco",
        Country:  "USA",
        Phone:    "+1-555-0123",
    }

    userService := services.NewUserService()

    // Validate the user
    err := userService.ValidateUser(user)
    if err != nil {
        fmt.Printf("FAIL: User validation failed: %v\n", err)
        os.Exit(1)
    }

    // Test user profile validation
    profile := &models.UserProfile{
        User: models.User{
            ID:       2,
            Name:     "Jane Doe",
            Email:    "jane@example.com",
            Age:      "30",
        },
        Bio:      "Software engineer",
        Website:  "https://jane.example.com",
        Twitter:  "@janedoe",
        LinkedIn: "linkedin.com/in/janedoe",
    }

    err = userService.ValidateUserProfile(profile)
    if err != nil {
        fmt.Printf("FAIL: User profile validation failed: %v\n", err)
        os.Exit(1)
    }

    fmt.Println("All validations passed!")
}