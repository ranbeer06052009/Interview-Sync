# 🚀 InterviewSync

**InterviewSync** is a high-performance, real-time collaborative code editor designed specifically for technical interviews. It goes beyond the standard "Google Docs clone" by implementing a Custom Operational Transformation (OT) engine, a full Remote Code Execution (RCE) environment, and a keystroke **Time-Travel** debugging tool.

🌐 **Live Demo:** [https://interview-sync-ecru.vercel.app/](https://interview-sync-ecru.vercel.app/)  
⚙️ **Backend API:** `https://interview-sync-production.up.railway.app`

## ✨ Features

- **Custom Operational Transformation (OT):** A highly concurrent Go WebSocket engine that handles race conditions and prevents index shifting when multiple users type simultaneously without relying on heavy third-party CRDT libraries.
- **Keystroke Time-Travel:** The backend operates as an event-sourced append-only log. Interviewers can drag a UI slider backward in time to watch the candidate's exact problem-solving and refactoring process stroke-by-stroke.
- **Remote Code Execution (RCE):** A fully isolated compilation environment running inside an Alpine Linux Docker container. It securely captures `stdout`/`stderr` and natively executes **Python, JavaScript, TypeScript, Go, Java, C++, and Rust**.
- **Anonymity Mode:** A toggle designed for blind interviews that masks PII (Personally Identifiable Information) with pseudonyms to prevent hiring bias.
- **Side-by-Side I/O:** Dedicated standard input and output text panes for algorithmic testing.

## 🛠️ Tech Stack

### Frontend
- **Framework:** Next.js 14 (React)
- **Editor:** Monaco Editor (The core engine of VS Code)
- **Styling:** Tailwind CSS

### Backend
- **Language:** Go (Golang)
- **Real-Time:** Gorilla WebSockets
- **Database:** PostgreSQL (with GORM)
- **Containerization:** Docker (Multi-stage Alpine builds)

## 🚀 Local Development Setup

To run this project locally, you will need Docker, Node.js, and Go installed.

### 1. Start the Database
The project uses Docker Compose to spin up a local PostgreSQL instance.
```bash
docker-compose up -d
```

### 2. Start the Backend
```bash
cd backend
go run .
```
*(The backend will automatically migrate the database schema and seed the default session on startup).*

### 3. Start the Frontend
```bash
cd frontend
npm install
npm run dev
```

Open `http://localhost:3000` in two different browser windows to test the real-time synchronization!

## 🛣️ Roadmap
- WebRTC integration for live audio/video chat.
- User authentication and persistent interview session dashboards.
- Save and playback complete interview sessions.

---
*Built to redefine the technical interview experience.*
