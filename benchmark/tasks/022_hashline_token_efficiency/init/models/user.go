package models

// User represents a user in the system
type User struct {
    ID       int
    Name     string
    Email    string
    // BUG: Age should be int, not string
    Age      string
    Address  string
    City     string
    Country  string
    Phone    string
}

// UserProfile extends User with additional profile information
type UserProfile struct {
    User
    Bio      string
    Website  string
    Twitter  string
    LinkedIn string
    GitHub   string
}

// UserStatus represents the current status of a user
type UserStatus string

const (
    UserStatusActive   UserStatus = "active"
    UserStatusInactive UserStatus = "inactive"
    UserStatusPending  UserStatus = "pending"
    UserStatusBlocked  UserStatus = "blocked"
)

// IsValid validates the user status
func (s UserStatus) IsValid() bool {
    switch s {
    case UserStatusActive, UserStatusInactive, UserStatusPending, UserStatusBlocked:
        return true
    default:
        return false
    }
}