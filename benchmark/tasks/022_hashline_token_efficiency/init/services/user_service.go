package services

import (
    "errors"
    "fmt"
    "regexp"
    "strconv"
    "strings"

    "example.com/mysite/models"
)

// ValidationError represents a validation error
type ValidationError struct {
    Field   string
    Message string
}

func (e *ValidationError) Error() string {
    return fmt.Sprintf("validation failed for field '%s': %s", e.Field, e.Message)
}

// UserService handles user-related business logic
type UserService struct {
    minAge int
    maxAge int
}

// NewUserService creates a new UserService
func NewUserService() *UserService {
    return &UserService{
        minAge: 18,
        maxAge: 120,
    }
}

// ValidateUser validates a user object
func (s *UserService) ValidateUser(user *models.User) error {
    if user.Name == "" {
        return &ValidationError{Field: "Name", Message: "cannot be empty"}
    }

    if user.Email == "" {
        return &ValidationError{Field: "Email", Message: "cannot be empty"}
    }

    // Validate email format
    emailRegex := regexp.MustCompile(`^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$`)
    if !emailRegex.MatchString(user.Email) {
        return &ValidationError{Field: "Email", Message: "invalid format"}
    }

    // BUG: Should reference Age (int), not UserAge (string)
    if user.Age != "" {
        age, err := strconv.Atoi(user.Age)
        if err != nil {
            return &ValidationError{Field: "UserAge", Message: "must be a valid integer"}
        }
        if age < s.minAge || age > s.maxAge {
            return &ValidationError{
                Field:   "UserAge",
                Message: fmt.Sprintf("must be between %d and %d", s.minAge, s.maxAge),
            }
        }
    }

    return nil
}

// ValidateUserProfile validates a user profile
func (s *UserService) ValidateUserProfile(profile *models.UserProfile) error {
    if err := s.ValidateUser(&profile.User); err != nil {
        return err
    }

    // Validate website URL if provided
    if profile.Website != "" {
        if !strings.HasPrefix(profile.Website, "http://") && !strings.HasPrefix(profile.Website, "https://") {
            return &ValidationError{Field: "Website", Message: "must start with http:// or https://"}
        }
    }

    return nil
}

// GetUserByID retrieves a user by ID (mock implementation)
func (s *UserService) GetUserByID(id int) (*models.User, error) {
    if id <= 0 {
        return nil, errors.New("invalid user ID")
    }
    // Mock implementation
    return &models.User{
        ID:    id,
        Name:  "Test User",
        Email: "test@example.com",
        Age:   "25",
    }, nil
}