package main

import (
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// Configuration - can be overridden by environment variables
var (
	uploadPath   string
	metadataFile string
	sessionsFile string
	usersFile    string
	serverPort   = ":8080"
)

func initPaths() {
	// Check environment variable first
	if path := os.Getenv("STORAGE_PATH"); path != "" {
		uploadPath = path
	} else if _, err := os.Stat("/sdcard"); err == nil {
		// Running on Termux/Android
		uploadPath = "/sdcard/home-server"
	} else {
		// Fallback for regular Linux
		uploadPath = "./data"
	}
	metadataFile = filepath.Join(uploadPath, ".metadata.json")
	sessionsFile = filepath.Join(uploadPath, ".sessions.json")
	usersFile = filepath.Join(uploadPath, ".users.json")

	if port := os.Getenv("PORT"); port != "" {
		serverPort = ":" + port
	}
}

// FileInfo stores metadata about each file
type FileInfo struct {
	Name       string    `json:"name"`
	UploadedBy string    `json:"uploaded_by"`
	UploadedAt time.Time `json:"uploaded_at"`
	Size       int64     `json:"size"`
	Shared     bool      `json:"shared"`
}

// Session stores user session data
type Session struct {
	Username  string    `json:"username"`
	CreatedAt time.Time `json:"created_at"`
}

// User stores user credentials
type User struct {
	Username     string    `json:"username"`
	PasswordHash string    `json:"password_hash"`
	CreatedAt    time.Time `json:"created_at"`
}

var (
	fileMetadata = make(map[string]FileInfo)
	sessions     = make(map[string]Session)
	users        = make(map[string]User)
	metaMutex    sync.RWMutex
	sessMutex    sync.RWMutex
	usersMutex   sync.RWMutex
)

// Log incoming requests
func logRequest(r *http.Request, status int) {
	log.Printf("[%s] %s %s %d\n", time.Now().Format(time.RFC3339), r.Method, r.URL.Path, status)
}

// Load metadata from file
func loadMetadata() error {
	metaMutex.Lock()
	defer metaMutex.Unlock()

	data, err := os.ReadFile(metadataFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // First run, no metadata file yet
		}
		return err
	}

	return json.Unmarshal(data, &fileMetadata)
}

// Save metadata to file
func saveMetadata() error {
	metaMutex.RLock()
	defer metaMutex.RUnlock()

	data, err := json.MarshalIndent(fileMetadata, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(metadataFile, data, 0644)
}

// Get session from cookie
func getSession(r *http.Request) *Session {
	cookie, err := r.Cookie("session_id")
	if err != nil {
		return nil
	}

	sessMutex.RLock()
	defer sessMutex.RUnlock()

	if session, ok := sessions[cookie.Value]; ok {
		return &session
	}
	return nil
}

// Load sessions from file
func loadSessions() error {
	sessMutex.Lock()
	defer sessMutex.Unlock()

	data, err := os.ReadFile(sessionsFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	return json.Unmarshal(data, &sessions)
}

// Save sessions to file
func saveSessions() error {
	sessMutex.RLock()
	defer sessMutex.RUnlock()

	data, err := json.MarshalIndent(sessions, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(sessionsFile, data, 0644)
}

// Create a new session
func createSession(username string) string {
	sessionID := fmt.Sprintf("%d", time.Now().UnixNano())

	sessMutex.Lock()
	sessions[sessionID] = Session{
		Username:  username,
		CreatedAt: time.Now(),
	}
	sessMutex.Unlock()

	saveSessions()
	return sessionID
}

// Load users from file
func loadUsers() error {
	usersMutex.Lock()
	defer usersMutex.Unlock()

	data, err := os.ReadFile(usersFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	return json.Unmarshal(data, &users)
}

// Save users to file
func saveUsers() error {
	usersMutex.RLock()
	defer usersMutex.RUnlock()

	data, err := json.MarshalIndent(users, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(usersFile, data, 0644)
}

// Check if user exists
func userExists(username string) bool {
	usersMutex.RLock()
	defer usersMutex.RUnlock()
	_, exists := users[username]
	return exists
}

// Create a new user
func createUser(username, password string) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}

	usersMutex.Lock()
	users[username] = User{
		Username:     username,
		PasswordHash: string(hash),
		CreatedAt:    time.Now(),
	}
	usersMutex.Unlock()

	return saveUsers()
}

// Verify user password
func verifyPassword(username, password string) bool {
	usersMutex.RLock()
	user, exists := users[username]
	usersMutex.RUnlock()

	if !exists {
		return false
	}

	err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password))
	return err == nil
}

// Render login page
func renderLogin(w http.ResponseWriter, r *http.Request) {
	tmpl, err := template.ParseFiles("templates/login.html")
	if err != nil {
		log.Printf("Error parsing login template: %v", err)
		http.Error(w, "Unable to load template", http.StatusInternalServerError)
		return
	}

	// Check for success message from signup
	success := r.URL.Query().Get("success")
	data := map[string]string{}
	if success == "1" {
		data["Success"] = "Account created! Please login."
	}

	tmpl.Execute(w, data)
	logRequest(r, http.StatusOK)
}

// Handle login
func handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	username := r.FormValue("username")
	password := r.FormValue("password")

	if username == "" {
		renderLoginWithError(w, r, "Please enter your name")
		return
	}

	if !userExists(username) {
		renderLoginWithError(w, r, "User not found. Please sign up first.")
		return
	}

	if !verifyPassword(username, password) {
		renderLoginWithError(w, r, "Incorrect password")
		return
	}

	sessionID := createSession(username)

	http.SetCookie(w, &http.Cookie{
		Name:     "session_id",
		Value:    sessionID,
		Path:     "/",
		MaxAge:   86400 * 30, // 30 days
		HttpOnly: true,
	})

	http.Redirect(w, r, "/", http.StatusSeeOther)
	logRequest(r, http.StatusSeeOther)
}

// Render login with error
func renderLoginWithError(w http.ResponseWriter, r *http.Request, errorMsg string) {
	tmpl, err := template.ParseFiles("templates/login.html")
	if err != nil {
		log.Printf("Error parsing login template: %v", err)
		http.Error(w, "Unable to load template", http.StatusInternalServerError)
		return
	}

	tmpl.Execute(w, map[string]string{"Error": errorMsg})
	logRequest(r, http.StatusOK)
}

// Render signup page
func renderSignup(w http.ResponseWriter, r *http.Request) {
	tmpl, err := template.ParseFiles("templates/signup.html")
	if err != nil {
		log.Printf("Error parsing signup template: %v", err)
		http.Error(w, "Unable to load template", http.StatusInternalServerError)
		return
	}

	tmpl.Execute(w, nil)
	logRequest(r, http.StatusOK)
}

// Handle signup
func handleSignup(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Redirect(w, r, "/signup", http.StatusSeeOther)
		return
	}

	username := r.FormValue("username")
	password := r.FormValue("password")
	confirmPassword := r.FormValue("confirm_password")

	// Validation
	if username == "" {
		renderSignupWithError(w, r, "Please enter a username")
		return
	}

	if len(username) < 2 {
		renderSignupWithError(w, r, "Username must be at least 2 characters")
		return
	}

	if len(password) < 4 {
		renderSignupWithError(w, r, "Password must be at least 4 characters")
		return
	}

	if password != confirmPassword {
		renderSignupWithError(w, r, "Passwords do not match")
		return
	}

	if userExists(username) {
		renderSignupWithError(w, r, "Username already taken")
		return
	}

	// Create user
	if err := createUser(username, password); err != nil {
		log.Printf("Error creating user: %v", err)
		renderSignupWithError(w, r, "Failed to create account")
		return
	}

	log.Printf("New user registered: %s", username)
	http.Redirect(w, r, "/login?success=1", http.StatusSeeOther)
	logRequest(r, http.StatusSeeOther)
}

// Render signup with error
func renderSignupWithError(w http.ResponseWriter, r *http.Request, errorMsg string) {
	tmpl, err := template.ParseFiles("templates/signup.html")
	if err != nil {
		log.Printf("Error parsing signup template: %v", err)
		http.Error(w, "Unable to load template", http.StatusInternalServerError)
		return
	}

	tmpl.Execute(w, map[string]string{"Error": errorMsg})
	logRequest(r, http.StatusOK)
}

// Handle logout
func handleLogout(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("session_id")
	if err == nil {
		sessMutex.Lock()
		delete(sessions, cookie.Value)
		sessMutex.Unlock()
		saveSessions()
	}

	http.SetCookie(w, &http.Cookie{
		Name:   "session_id",
		Value:  "",
		Path:   "/",
		MaxAge: -1,
	})

	http.Redirect(w, r, "/login", http.StatusSeeOther)
	logRequest(r, http.StatusSeeOther)
}

// Auth middleware
func requireAuth(handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session := getSession(r)
		if session == nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		handler(w, r)
	}
}

// Get file list with metadata (filtered by viewer)
func getFilesWithMetadata(viewer string) []FileInfo {
	entries, err := os.ReadDir(uploadPath)
	if err != nil {
		log.Printf("Error reading directory: %v", err)
		return nil
	}

	metaMutex.RLock()
	defer metaMutex.RUnlock()

	var files []FileInfo
	for _, entry := range entries {
		if entry.IsDir() || entry.Name() == ".metadata.json" || entry.Name() == ".sessions.json" || entry.Name() == ".users.json" {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			continue
		}

		var file FileInfo
		if meta, ok := fileMetadata[entry.Name()]; ok {
			file = meta
			file.Size = info.Size() // Update size
		} else {
			// File exists but no metadata (legacy or manually added)
			file = FileInfo{
				Name:       entry.Name(),
				UploadedBy: "Unknown",
				UploadedAt: info.ModTime(),
				Size:       info.Size(),
			}
		}

		// Filter: Include if owned by viewer OR shared
		// "Unknown" uploaders are visible to everyone? Or only shared?
		// Let's hide "Unknown" unless shared, or maybe show everything for backward compatibility?
		// Better safe: Only show "Mine" or "Shared".
		if file.UploadedBy == viewer || file.Shared {
			files = append(files, file)
		}
	}

	return files
}

// Template data for file list page
type PageData struct {
	Username string
	Files    []FileInfo
}

// Render main files page
func renderFilesPage(w http.ResponseWriter, r *http.Request) {
	session := getSession(r)
	if session == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	files := getFilesWithMetadata(session.Username)

	tmpl, err := template.New("files.html").Funcs(template.FuncMap{
		"formatSize": formatSize,
		"formatDate": formatDate,
	}).ParseFiles("templates/files.html")

	if err != nil {
		log.Printf("Error parsing files template: %v", err)
		http.Error(w, "Unable to load template", http.StatusInternalServerError)
		logRequest(r, http.StatusInternalServerError)
		return
	}

	data := PageData{
		Username: session.Username,
		Files:    files,
	}

	tmpl.Execute(w, data)
	logRequest(r, http.StatusOK)
}

// Format file size for display
func formatSize(size int64) string {
	const (
		KB = 1024
		MB = 1024 * KB
		GB = 1024 * MB
	)

	switch {
	case size >= GB:
		return fmt.Sprintf("%.1f GB", float64(size)/GB)
	case size >= MB:
		return fmt.Sprintf("%.1f MB", float64(size)/MB)
	case size >= KB:
		return fmt.Sprintf("%.1f KB", float64(size)/KB)
	default:
		return fmt.Sprintf("%d B", size)
	}
}

// Format date for display
func formatDate(t time.Time) string {
	return t.Format("Jan 2, 2006 3:04 PM")
}

// Upload single or multiple files
func uploadFiles(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Invalid request method", http.StatusMethodNotAllowed)
		logRequest(r, http.StatusMethodNotAllowed)
		return
	}

	session := getSession(r)
	if session == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	// Parse multipart form (max 100MB)
	err := r.ParseMultipartForm(100 << 20)
	if err != nil {
		log.Printf("Error parsing multipart form: %v", err)
		http.Error(w, "Failed to parse form", http.StatusBadRequest)
		logRequest(r, http.StatusBadRequest)
		return
	}

	files := r.MultipartForm.File["files"]
	if len(files) == 0 {
		// Try single file field
		file, header, err := r.FormFile("file")
		if err != nil {
			http.Error(w, "No files provided", http.StatusBadRequest)
			logRequest(r, http.StatusBadRequest)
			return
		}
		defer file.Close()

		newName := r.FormValue("newFileName")
		if newName == "" {
			newName = header.Filename
		}

		if err := saveUploadedFile(file, newName, header.Size, session.Username); err != nil {
			http.Error(w, "Failed to save file", http.StatusInternalServerError)
			logRequest(r, http.StatusInternalServerError)
			return
		}

		log.Printf("File uploaded by %s: %s", session.Username, newName)
	} else {
		// Multiple files
		for _, fileHeader := range files {
			file, err := fileHeader.Open()
			if err != nil {
				log.Printf("Error opening file %s: %v", fileHeader.Filename, err)
				continue
			}

			if err := saveUploadedFile(file, fileHeader.Filename, fileHeader.Size, session.Username); err != nil {
				log.Printf("Error saving file %s: %v", fileHeader.Filename, err)
				file.Close()
				continue
			}

			file.Close()
			log.Printf("File uploaded by %s: %s", session.Username, fileHeader.Filename)
		}
	}

	http.Redirect(w, r, "/", http.StatusSeeOther)
	logRequest(r, http.StatusSeeOther)
}

// Save uploaded file and update metadata
func saveUploadedFile(file io.Reader, filename string, size int64, username string) error {
	filePath := filepath.Join(uploadPath, filename)

	dest, err := os.Create(filePath)
	if err != nil {
		return err
	}
	defer dest.Close()

	written, err := io.Copy(dest, file)
	if err != nil {
		return err
	}

	// Update metadata
	metaMutex.Lock()
	fileMetadata[filename] = FileInfo{
		Name:       filename,
		UploadedBy: username,
		UploadedAt: time.Now(),
		Size:       written,
	}
	metaMutex.Unlock()

	return saveMetadata()
}

// Rename file
func renameFile(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Invalid request method", http.StatusMethodNotAllowed)
		logRequest(r, http.StatusMethodNotAllowed)
		return
	}

	session := getSession(r)
	if session == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	oldName := r.FormValue("oldFileName")
	newName := r.FormValue("newFileName")

	if oldName == "" || newName == "" {
		http.Error(w, "File names cannot be empty", http.StatusBadRequest)
		logRequest(r, http.StatusBadRequest)
		return
	}

	// Check permissions
	metaMutex.RLock()
	meta, ok := fileMetadata[oldName]
	metaMutex.RUnlock()

	if ok && meta.UploadedBy != session.Username {
		http.Error(w, "You don't have permission to rename this file", http.StatusForbidden)
		return
	}

	oldPath := filepath.Join(uploadPath, oldName)
	newPath := filepath.Join(uploadPath, newName)

	err := os.Rename(oldPath, newPath)
	if err != nil {
		log.Printf("Failed to rename file from %s to %s: %v", oldName, newName, err)
		http.Error(w, "Failed to rename file", http.StatusInternalServerError)
		logRequest(r, http.StatusInternalServerError)
		return
	}

	// Update metadata
	metaMutex.Lock()
	if meta, ok := fileMetadata[oldName]; ok {
		meta.Name = newName
		fileMetadata[newName] = meta
		delete(fileMetadata, oldName)
	}
	metaMutex.Unlock()
	saveMetadata()

	log.Printf("File renamed by %s: %s -> %s", session.Username, oldName, newName)
	http.Redirect(w, r, "/", http.StatusSeeOther)
	logRequest(r, http.StatusSeeOther)
}

// Delete file
func deleteFile(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Invalid request method", http.StatusMethodNotAllowed)
		logRequest(r, http.StatusMethodNotAllowed)
		return
	}

	session := getSession(r)
	if session == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	fileName := r.FormValue("fileName")
	if fileName == "" {
		http.Error(w, "File name cannot be empty", http.StatusBadRequest)
		logRequest(r, http.StatusBadRequest)
		return
	}

	// Check permissions
	metaMutex.RLock()
	meta, ok := fileMetadata[fileName]
	metaMutex.RUnlock()

	if ok && meta.UploadedBy != session.Username {
		http.Error(w, "You don't have permission to delete this file", http.StatusForbidden)
		return
	}

	filePath := filepath.Join(uploadPath, fileName)

	err := os.Remove(filePath)
	if err != nil {
		log.Printf("Failed to delete file %s: %v", fileName, err)
		http.Error(w, "Failed to delete file", http.StatusInternalServerError)
		logRequest(r, http.StatusInternalServerError)
		return
	}

	// Remove from metadata
	metaMutex.Lock()
	delete(fileMetadata, fileName)
	metaMutex.Unlock()
	saveMetadata()

	log.Printf("File deleted by %s: %s", session.Username, fileName)
	http.Redirect(w, r, "/", http.StatusSeeOther)
	logRequest(r, http.StatusSeeOther)
}

// Download file
func downloadFile(w http.ResponseWriter, r *http.Request) {
	session := getSession(r)
	if session == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	fileName := r.URL.Query().Get("file")
	if fileName == "" {
		http.Error(w, "File not specified", http.StatusBadRequest)
		logRequest(r, http.StatusBadRequest)
		return
	}

	// Permission check?
	// If it's shared OR owned by user
	// But getFilesWithMetadata already filters list.
	// If user manually types URL for someone else's private file, they shouldn't see it.

	metaMutex.RLock()
	meta, ok := fileMetadata[fileName]
	metaMutex.RUnlock()

	if ok {
		if meta.UploadedBy != session.Username && !meta.Shared {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
	}

	filePath := filepath.Join(uploadPath, fileName)
	w.Header().Set("Content-Disposition", "attachment; filename="+fileName)
	http.ServeFile(w, r, filePath)
	log.Printf("File downloaded by %s: %s", session.Username, fileName)
	logRequest(r, http.StatusOK)
}

// Toggle file sharing
func toggleShare(w http.ResponseWriter, r *http.Request) {
	session := getSession(r)
	if session == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	fileName := r.FormValue("fileName")
	if fileName == "" {
		http.Error(w, "File name required", http.StatusBadRequest)
		return
	}

	metaMutex.Lock()
	if meta, ok := fileMetadata[fileName]; ok {
		if meta.UploadedBy != session.Username {
			metaMutex.Unlock()
			http.Error(w, "Permission denied", http.StatusForbidden)
			return
		}

		meta.Shared = !meta.Shared
		fileMetadata[fileName] = meta
		metaMutex.Unlock()
		saveMetadata()

		// Return JSON response
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]bool{"shared": meta.Shared})
		log.Printf("File sharing toggled by %s: %s (shared: %v)", session.Username, fileName, meta.Shared)
	} else {
		metaMutex.Unlock()
		http.Error(w, "File not found", http.StatusNotFound)
	}
}

// Serve shared file (public, no auth)
func serveSharedFile(w http.ResponseWriter, r *http.Request) {
	// Extract filename from /shared/{filename}
	fileName := r.URL.Path[len("/shared/"):]
	if fileName == "" {
		http.Error(w, "File not specified", http.StatusBadRequest)
		return
	}

	metaMutex.RLock()
	meta, exists := fileMetadata[fileName]
	metaMutex.RUnlock()

	if !exists {
		http.Error(w, "File not found", http.StatusNotFound)
		return
	}

	if !meta.Shared {
		http.Error(w, "This file is not shared", http.StatusForbidden)
		return
	}

	filePath := filepath.Join(uploadPath, fileName)
	http.ServeFile(w, r, filePath)
	log.Printf("Shared file accessed: %s", fileName)
}

func main() {
	// Initialize paths based on environment
	initPaths()

	// Ensure the upload directory exists
	if err := os.MkdirAll(uploadPath, os.ModePerm); err != nil {
		log.Fatalf("Failed to create upload directory: %v", err)
	}

	// Load existing metadata
	if err := loadMetadata(); err != nil {
		log.Printf("Warning: Could not load metadata: %v", err)
	}

	// Load existing sessions
	if err := loadSessions(); err != nil {
		log.Printf("Warning: Could not load sessions: %v", err)
	}

	// Load existing users
	if err := loadUsers(); err != nil {
		log.Printf("Warning: Could not load users: %v", err)
	}

	// Set up routes
	http.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			handleLogin(w, r)
		} else {
			renderLogin(w, r)
		}
	})
	http.HandleFunc("/signup", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			handleSignup(w, r)
		} else {
			renderSignup(w, r)
		}
	})
	http.HandleFunc("/logout", handleLogout)
	http.HandleFunc("/", requireAuth(renderFilesPage))
	http.HandleFunc("/upload", requireAuth(uploadFiles))
	http.HandleFunc("/rename", requireAuth(renameFile))
	http.HandleFunc("/delete", requireAuth(deleteFile))
	http.HandleFunc("/download", requireAuth(downloadFile))
	http.HandleFunc("/share", toggleShare)
	http.HandleFunc("/shared/", serveSharedFile)
	http.HandleFunc("/files/", requireAuth(handleServeFile))

	// Start server
	fmt.Printf("Server started at %s\n", serverPort)
	fmt.Printf("Files stored at: %s\n", uploadPath)
	log.Printf("Starting server on %s", serverPort)
	log.Fatal(http.ListenAndServe(serverPort, nil))
}

// Serve files with permission check
func handleServeFile(w http.ResponseWriter, r *http.Request) {
	session := getSession(r)
	if session == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	fileName := strings.TrimPrefix(r.URL.Path, "/files/")
	if fileName == "" {
		http.Error(w, "File not specified", http.StatusBadRequest)
		return
	}

	// Check permissions
	metaMutex.RLock()
	meta, ok := fileMetadata[fileName]
	metaMutex.RUnlock()

	if !ok {
		// Fail closed: If we don't know who owns it, don't serve it.
		// Exception: If you really want "Unknown" files to be public, change this.
		// For now, strict privacy.
		log.Printf("Access denied to %s (no metadata) for user %s", fileName, session.Username)
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	if meta.UploadedBy != session.Username && !meta.Shared {
		log.Printf("Access denied to %s (private) for user %s", fileName, session.Username)
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	filePath := filepath.Join(uploadPath, fileName)
	http.ServeFile(w, r, filePath)
}
