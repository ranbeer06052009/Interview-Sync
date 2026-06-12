package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

var db *gorm.DB

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow all origins for dev
	},
}

// Client represents a connected user
type Client struct {
	Conn      *websocket.Conn
	SessionID string
	UserID    string
}

// IncomingEvent represents a message from the client
type IncomingEvent struct {
	Operation   OperationType `json:"operation"`
	Position    int           `json:"position"`
	Content     *string       `json:"content"`
	BaseVersion int           `json:"baseVersion"` // The version of the document the client based this edit on
}

// Hub maintains the set of active clients and broadcasts messages
type Hub struct {
	clients    map[*Client]bool
	broadcast  chan messagePayload
	register   chan *Client
	unregister chan *Client
	mu         sync.Mutex
}

type messagePayload struct {
	SessionID string
	Data      []byte
	Sender    *Client
}

var hub = Hub{
	broadcast:  make(chan messagePayload),
	register:   make(chan *Client),
	unregister: make(chan *Client),
	clients:    make(map[*Client]bool),
}

func initDB() {
	var err error
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "host=localhost user=admin password=password dbname=interviewsync port=5432 sslmode=disable"
	}
	db, err = gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}

	err = db.AutoMigrate(&InterviewSession{}, &User{}, &DocumentEvent{})
	if err != nil {
		log.Fatalf("Failed to migrate database: %v", err)
	}

	// Seed default session
	defaultSessionID := "123e4567-e89b-12d3-a456-426614174000"
	var count int64
	db.Model(&InterviewSession{}).Where("id = ?", defaultSessionID).Count(&count)
	if count == 0 {
		db.Create(&InterviewSession{
			ID:          defaultSessionID,
			Title:       "Default Interview Session",
			IsAnonymous: false,
		})
	}

	fmt.Println("Database connected and migrated.")
}

func reconstructDocument(sessionID string, targetTime time.Time) (string, error) {
	var events []DocumentEvent
	result := db.Where("session_id = ? AND timestamp <= ?", sessionID, targetTime).
		Order("version asc").
		Find(&events)
	
	if result.Error != nil {
		return "", result.Error
	}

	doc := ""
	for _, event := range events {
		if event.Operation == OperationInsert && event.Content != nil {
			pos := event.Position
			if pos > len(doc) {
				pos = len(doc)
			}
			doc = doc[:pos] + *event.Content + doc[pos:]
		} else if event.Operation == OperationDelete {
			pos := event.Position
			if pos >= 0 && pos < len(doc) {
				doc = doc[:pos] + doc[pos+1:]
			}
		}
	}
	return doc, nil
}

// processOT transforms the incoming position if there are concurrent events
func processOT(sessionID string, baseVersion int, position int) (int, error) {
	var concurrentEvents []DocumentEvent
	result := db.Where("session_id = ? AND version > ?", sessionID, baseVersion).
		Order("version asc").
		Find(&concurrentEvents)
	
	if result.Error != nil {
		return position, result.Error
	}

	transformedPos := position
	for _, e := range concurrentEvents {
		if e.Operation == OperationInsert && e.Content != nil {
			// If a concurrent insert happened before or at our position, our position shifts right
			if e.Position <= transformedPos {
				transformedPos += len(*e.Content)
			}
		} else if e.Operation == OperationDelete {
			// If a concurrent delete happened before our position, our position shifts left
			if e.Position < transformedPos {
				transformedPos -= 1 // assuming single char delete for this simplified OT
			}
		}
	}
	return transformedPos, nil
}

var eventMu sync.Mutex // A global mutex for events in dev, production would use redis/db locks per session

func handleWebSocket(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("sessionId")
	userID := r.URL.Query().Get("userId")

	if sessionID == "" || userID == "" {
		http.Error(w, "sessionId and userId are required", http.StatusBadRequest)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println("Upgrade error:", err)
		return
	}

	client := &Client{Conn: conn, SessionID: sessionID, UserID: userID}
	
	// Create the user if it doesn't exist to satisfy the foreign key constraint
	var userCount int64
	db.Model(&User{}).Where("id = ?", userID).Count(&userCount)
	if userCount == 0 {
		db.Create(&User{
			ID:        userID,
			RealName:  "Candidate/Interviewer",
			SessionID: sessionID,
		})
	}

	hub.register <- client

	go func() {
		defer func() {
			hub.unregister <- client
			conn.Close()
		}()
		for {
			_, message, err := conn.ReadMessage()
			if err != nil {
				log.Println("Read error:", err)
				break
			}
			
			var inc IncomingEvent
			if err := json.Unmarshal(message, &inc); err == nil {
				
				// Critical Section for OT and Event saving
				eventMu.Lock()
				
				// Apply OT
				transformedPos, _ := processOT(sessionID, inc.BaseVersion, inc.Position)
				
				var count int64
				db.Model(&DocumentEvent{}).Where("session_id = ?", sessionID).Count(&count)
				newVersion := int(count) + 1
				
				finalEvent := DocumentEvent{
					SessionID: sessionID,
					UserID:    userID,
					Operation: inc.Operation,
					Position:  transformedPos,
					Content:   inc.Content,
					Timestamp: time.Now(),
					Version:   newVersion,
				}
				
				db.Create(&finalEvent)
				
				eventMu.Unlock()
				
				// Broadcast the transformed event
				outMsg, _ := json.Marshal(finalEvent)
				
				hub.broadcast <- messagePayload{
					SessionID: sessionID,
					Data:      outMsg,
					Sender:    client,
				}
			}
		}
	}()
}

func runHub() {
	for {
		select {
		case client := <-hub.register:
			hub.mu.Lock()
			hub.clients[client] = true
			hub.mu.Unlock()
		case client := <-hub.unregister:
			hub.mu.Lock()
			if _, ok := hub.clients[client]; ok {
				delete(hub.clients, client)
			}
			hub.mu.Unlock()
		case payload := <-hub.broadcast:
			hub.mu.Lock()
			for client := range hub.clients {
				// Broadcast to all clients in the same session except sender
				if client.SessionID == payload.SessionID && client != payload.Sender {
					client.Conn.WriteMessage(websocket.TextMessage, payload.Data)
				}
			}
			hub.mu.Unlock()
		}
	}
}

func handleTimeTravel(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("sessionId")
	timestampStr := r.URL.Query().Get("timestamp") 

	if sessionID == "" || timestampStr == "" {
		http.Error(w, "sessionId and timestamp are required", http.StatusBadRequest)
		return
	}

	targetTime, err := time.Parse(time.RFC3339, timestampStr)
	if err != nil {
		http.Error(w, "Invalid timestamp format", http.StatusBadRequest)
		return
	}

	doc, err := reconstructDocument(sessionID, targetTime)
	if err != nil {
		http.Error(w, "Failed to reconstruct document", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	json.NewEncoder(w).Encode(map[string]interface{}{"document": doc})
}

func handleGetVersion(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("sessionId")
	var count int64
	db.Model(&DocumentEvent{}).Where("session_id = ?", sessionID).Count(&count)
	
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	json.NewEncoder(w).Encode(map[string]interface{}{"version": count})
}

func handleGetEvents(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("sessionId")
	var events []DocumentEvent
	db.Where("session_id = ?", sessionID).Order("version asc").Find(&events)
	
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	json.NewEncoder(w).Encode(events)
}

func handleSessionDetails(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("sessionId")
	var session InterviewSession
	if err := db.Preload("Participants").First(&session, "id = ?", sessionID).Error; err != nil {
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	json.NewEncoder(w).Encode(session)
}

func handleToggleAnonymity(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	sessionID := r.URL.Query().Get("sessionId")
	var session InterviewSession
	if err := db.First(&session, "id = ?", sessionID).Error; err != nil {
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}

	session.IsAnonymous = !session.IsAnonymous
	db.Save(&session)

	// Broadcast an anonymity toggled event (for simplicity, we could use the hub, but polling or standard WS message works)
	// For now, the client will re-fetch
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	json.NewEncoder(w).Encode(session)
}

func handleLanguageChange(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	sessionID := r.URL.Query().Get("sessionId")
	var req struct {
		Language string `json:"language"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid payload", http.StatusBadRequest)
		return
	}

	var session InterviewSession
	if err := db.First(&session, "id = ?", sessionID).Error; err != nil {
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}

	session.Language = req.Language
	db.Save(&session)

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	json.NewEncoder(w).Encode(session)
}

type ExecuteRequest struct {
	Code     string `json:"code"`
	Language string `json:"language"`
	Input    string `json:"input"`
}

func handleExecute(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req ExecuteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid payload", http.StatusBadRequest)
		return
	}

	lang := req.Language
	var cmd *exec.Cmd
	tmpDir, err := os.MkdirTemp("", "interviewsync-*")
	if err != nil {
		http.Error(w, "Failed to create temp dir", http.StatusInternalServerError)
		return
	}
	defer os.RemoveAll(tmpDir)

	if lang == "python" {
		filePath := filepath.Join(tmpDir, "main.py")
		os.WriteFile(filePath, []byte(req.Code), 0644)
		cmd = exec.Command("python", filePath)
	} else if lang == "javascript" {
		filePath := filepath.Join(tmpDir, "main.js")
		os.WriteFile(filePath, []byte(req.Code), 0644)
		cmd = exec.Command("node", filePath)
	} else if lang == "typescript" {
		filePath := filepath.Join(tmpDir, "main.ts")
		os.WriteFile(filePath, []byte(req.Code), 0644)
		cmd = exec.Command("ts-node", filePath)
	} else if lang == "go" {
		filePath := filepath.Join(tmpDir, "main.go")
		os.WriteFile(filePath, []byte(req.Code), 0644)
		cmd = exec.Command("go", "run", filePath)
	} else if lang == "rust" {
		filePath := filepath.Join(tmpDir, "main.rs")
		os.WriteFile(filePath, []byte(req.Code), 0644)
		outExe := filepath.Join(tmpDir, "main")
		
		compileCmd := exec.Command("rustc", filePath, "-o", outExe)
		var compileErr bytes.Buffer
		compileCmd.Stderr = &compileErr
		if err := compileCmd.Run(); err != nil {
			errStr := compileErr.String()
			if errStr == "" {
				errStr = err.Error()
			}
			json.NewEncoder(w).Encode(map[string]interface{}{
				"run": map[string]string{
					"stdout": "",
					"stderr": "Compilation Error:\n" + errStr,
				},
			})
			return
		}
		cmd = exec.Command(outExe)
	} else if lang == "cpp" || lang == "c++" {
		filePath := filepath.Join(tmpDir, "main.cpp")
		os.WriteFile(filePath, []byte(req.Code), 0644)
		outExe := filepath.Join(tmpDir, "a.exe")
		
		compileCmd := exec.Command("g++", filePath, "-o", outExe)
		var compileErr bytes.Buffer
		compileCmd.Stderr = &compileErr
		if err := compileCmd.Run(); err != nil {
			errStr := compileErr.String()
			if errStr == "" {
				errStr = err.Error()
			}
			json.NewEncoder(w).Encode(map[string]interface{}{
				"run": map[string]string{
					"stdout": "",
					"stderr": "Compilation Error:\n" + errStr,
				},
			})
			return
		}
		cmd = exec.Command(outExe)
	} else if lang == "java" {
		filePath := filepath.Join(tmpDir, "Main.java")
		os.WriteFile(filePath, []byte(req.Code), 0644)
		
		compileCmd := exec.Command("javac", filePath)
		var compileErr bytes.Buffer
		compileCmd.Stderr = &compileErr
		if err := compileCmd.Run(); err != nil {
			errStr := compileErr.String()
			if errStr == "" {
				errStr = err.Error()
			}
			json.NewEncoder(w).Encode(map[string]interface{}{
				"run": map[string]string{
					"stdout": "",
					"stderr": "Compilation Error:\n" + errStr,
				},
			})
			return
		}
		cmd = exec.Command("java", "-cp", tmpDir, "Main")
	} else if lang == "html" || lang == "css" {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"message": "HTML and CSS are markup/styling languages and cannot be executed in a standard IO terminal. You can use this mode for syntax-highlighted collaboration.",
		})
		return
	} else {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"message": "Local execution currently supports Python, JavaScript, TypeScript, Go, C++, Rust, and Java.",
		})
		return
	}

	if req.Input != "" {
		cmd.Stdin = bytes.NewBufferString(req.Input)
	}

	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	err = cmd.Run()
	// Ignore err here since non-zero exit codes will return an err, but we still want to read stderrBuf

	response := map[string]interface{}{
		"run": map[string]string{
			"stdout": stdoutBuf.String(),
			"stderr": stderrBuf.String(),
		},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"status": "online", "message": "InterviewSync Go WebSocket Engine is running!"}`))
}

func main() {
	go initDB()
	
	go runHub()

	http.HandleFunc("/", handleRoot)
	http.HandleFunc("/ws", handleWebSocket)
	http.HandleFunc("/api/time-travel", handleTimeTravel)
	http.HandleFunc("/api/version", handleGetVersion) 
	http.HandleFunc("/api/events", handleGetEvents)   
	http.HandleFunc("/api/session", handleSessionDetails)
	http.HandleFunc("/api/session/toggle-anonymity", handleToggleAnonymity)
	http.HandleFunc("/api/session/language", handleLanguageChange)
	http.HandleFunc("/api/execute", handleExecute)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	fmt.Println("Server started on :" + port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}
