package main

import (
	"time"
)

type OperationType string

const (
	OperationInsert     OperationType = "INSERT"
	OperationDelete     OperationType = "DELETE"
	OperationCursorMove OperationType = "CURSOR_MOVE"
)

type InterviewSession struct {
	ID          string    `gorm:"type:uuid;primary_key;default:gen_random_uuid()"`
	Title       string    `gorm:"not null"`
	Language    string    `gorm:"default:'javascript'"`
	IsAnonymous bool      `gorm:"default:false"`
	CreatedAt   time.Time `gorm:"autoCreateTime"`

	Participants []User          `gorm:"foreignKey:SessionID"`
	Events       []DocumentEvent `gorm:"foreignKey:SessionID"`
}

type User struct {
	ID         string  `gorm:"type:uuid;primary_key;default:gen_random_uuid()"`
	RealName   string  `gorm:"not null"`
	PseudoName *string `gorm:"default:null"`
	SessionID  string  `gorm:"type:uuid;not null"`

	Events []DocumentEvent `gorm:"foreignKey:UserID"`
}

type DocumentEvent struct {
	ID        string        `gorm:"type:uuid;primary_key;default:gen_random_uuid()" json:"id"`
	SessionID string        `gorm:"type:uuid;not null;index:idx_session_version,priority:1" json:"sessionId"`
	UserID    string        `gorm:"type:uuid;not null" json:"userId"`
	
	Operation OperationType `gorm:"not null" json:"operation"`
	Position  int           `gorm:"not null" json:"position"`
	Content   *string       `gorm:"default:null" json:"content"`
	
	Timestamp time.Time     `gorm:"autoCreateTime;index" json:"timestamp"`
	Version   int           `gorm:"not null;index:idx_session_version,priority:2" json:"version"`
}
