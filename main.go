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
	} else {
		// Try to detect Termux/Android environment and prioritize SD card storage
		foundSD := false
		
		// Method 1: Check for Termux's external storage (most reliable for Termux)
		home := os.Getenv("HOME")
		if home != "" {
			// Try Termux's external storage (SD card) first
			termuxSD := filepath.Join(home, "storage/external-1/home-server")
			if _, err := os.Stat(filepath.Join(home, "storage/external-1")); err == nil {
				if err := os.MkdirAll(termuxSD, 0755); err == nil {
					uploadPath = termuxSD
					foundSD = true
					log.Printf("Using Termux external storage (SD card): %s", uploadPath)
					log.Printf("NOTE: Files stored here should be visible in Android file managers")
				}
			}
			
			// If no external storage found, try internal storage
			if !foundSD {
				termuxInternal := filepath.Join(home, "storage/shared/home-server")
				if _, err := os.Stat(filepath.Join(home, "storage/shared")); err == nil {
					if err := os.MkdirAll(termuxInternal, 0755); err == nil {
						uploadPath = termuxInternal
						foundSD = true
						log.Printf("Using Termux internal shared storage: %s", uploadPath)
						log.Printf("NOTE: Files stored here should be visible in Android file managers at /storage/emulated/0/home-server")
					}
				}
			}
			
			// If Termux storage is not accessible, check if termux-setup-storage needs to be run
			if !foundSD {
				if _, err := os.Stat(filepath.Join(home, "storage")); err != nil {
					log.Printf("Termux storage not accessible. Run 'termux-setup-storage' to grant storage permissions.")
					log.Printf("Without storage permissions, files will only be accessible within Termux at: %s/data", home)
				}
			}
		}
		
		// Method 2: Check for Android's /storage directory (alternative approach)
		if !foundSD {
			if _, err := os.Stat("/storage"); err == nil {
				if entries, err := os.ReadDir("/storage"); err == nil {
					for _, entry := range entries {
						if entry.IsDir() && entry.Name() != "emulated" && entry.Name() != "self" {
							// Likely a physical SD card (e.g., ABCD-1234)
							sdPath := filepath.Join("/storage", entry.Name(), "Android/data/com.termux/files/home-server")
							if err := os.MkdirAll(sdPath, 0755); err == nil {
								uploadPath = sdPath
								foundSD = true
								log.Printf("Using Android SD card storage: %s", uploadPath)
								break
							}
						}
					}
				}
			}
		}
		
		// Method 3: Fallback to standard Android shared storage
		if !foundSD {
			uploadPath = "/sdcard/home-server"
			log.Printf("Using Android internal shared storage: %s", uploadPath)
		}
		
		// Method 4: Fallback for regular Linux
		if !foundSD {
			uploadPath = "data"
			log.Printf("Using local data directory: %s", uploadPath)
		}
	}
	
	uploadPath = filepath.Clean(uploadPath)
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
	IsDir      bool      `json:"is_dir"`
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
		SameSite: http.SameSiteLaxMode,
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

	if len(password) < 8 {
		renderSignupWithError(w, r, "Password must be at least 8 characters")
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
func getFilesWithMetadata(viewer string, currentPath string) []FileInfo {
	fullPath := filepath.Join(uploadPath, currentPath)

	// Prevent directory traversal
	rel, err := filepath.Rel(uploadPath, fullPath)
	if err != nil || strings.HasPrefix(rel, "..") {
		log.Printf("Traversal attempt or error: path=%s, uploadPath=%s, err=%v", fullPath, uploadPath, err)
		return nil
	}

	entries, err := os.ReadDir(fullPath)
	if err != nil {
		log.Printf("Error reading directory: %v", err)
		return nil
	}

	metaMutex.RLock()
	defer metaMutex.RUnlock()

	var files []FileInfo

	// Add ".." entry if not root
	if currentPath != "" && currentPath != "." {
		parent := filepath.Dir(currentPath)
		if parent == "." {
			parent = ""
		}
		files = append(files, FileInfo{
			Name:       "..",
			IsDir:      true,
			UploadedBy: "",
			// We can encode parent path in Name or handle it in frontend.
			// Standard way: Name is "..", frontend logic handles link.
		})
	}

	for _, entry := range entries {
		if entry.Name() == ".metadata.json" || entry.Name() == ".sessions.json" || entry.Name() == ".users.json" {
			continue
		}

		relPath := filepath.Join(currentPath, entry.Name())
		info, err := entry.Info()
		if err != nil {
			continue
		}

		var file FileInfo
		// Metadata key uses relative path
		if meta, ok := fileMetadata[relPath]; ok {
			file = meta
			file.Size = info.Size() // Update size
			file.IsDir = entry.IsDir()
			// If name stored in meta is full path, we might want to show only basename
			file.Name = entry.Name()
		} else {
			// File exists but no metadata (legacy or manually added)
			file = FileInfo{
				Name:       entry.Name(),
				UploadedBy: "Unknown",
				UploadedAt: info.ModTime(),
				Size:       info.Size(),
				IsDir:      entry.IsDir(),
			}
		}

		// Filter: Include if owned by viewer OR shared
		// For directories, we might want to show them if they contain shared files?
		// For simplicity: If it's a directory, currently show it.
		// Or assume if I uploaded the folder, I own it.
		// If "Unknown" (created manually), show it.
		if file.UploadedBy == viewer || file.UploadedBy == "Unknown" || file.Shared || file.IsDir {
			files = append(files, file)
		}
	}

	return files
}

// Template data for file list page
type PageData struct {
	Username    string
	Files       []FileInfo
	CurrentPath string
}

// Render main files page
func renderFilesPage(w http.ResponseWriter, r *http.Request) {
	session := getSession(r)
	if session == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	currentPath := r.URL.Query().Get("path")
	// Sanitize input
	currentPath = filepath.Clean(currentPath)
	if currentPath == "." || currentPath == "/" {
		currentPath = ""
	}
	// simple check to prevent breaking out of root (Clean handles much, but prefix check is good)
	if strings.Contains(currentPath, "..") {
		currentPath = ""
	}

	files := getFilesWithMetadata(session.Username, currentPath)

	tmpl, err := template.New("files.html").Funcs(template.FuncMap{
		"formatSize": formatSize,
		"formatDate": formatDate,
		"dir":        filepath.Dir,
	}).ParseFiles("templates/files.html")

	if err != nil {
		log.Printf("Error parsing files template: %v", err)
		http.Error(w, "Unable to load template", http.StatusInternalServerError)
		logRequest(r, http.StatusInternalServerError)
		return
	}

	data := PageData{
		Username:    session.Username,
		Files:       files,
		CurrentPath: currentPath,
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

	// Parse multipart form (max 500MB for large uploads)
	// Note: This limit applies to the entire request body, not per file
	err := r.ParseMultipartForm(500 << 20)
	if err != nil {
		log.Printf("Error parsing multipart form: %v", err)
		// Provide more specific error message
		if strings.Contains(err.Error(), "request body too large") {
			http.Error(w, "Upload too large. Maximum total size: 500MB", http.StatusRequestEntityTooLarge)
		} else if strings.Contains(err.Error(), "EOF") {
			http.Error(w, "Upload interrupted or incomplete", http.StatusBadRequest)
		} else {
			http.Error(w, "Failed to parse form: "+err.Error(), http.StatusBadRequest)
		}
		logRequest(r, http.StatusBadRequest)
		return
	}

	files := r.MultipartForm.File["files"]
	
	// Limit the number of files that can be uploaded at once to prevent memory issues
	const maxFilesAtOnce = 1000
	if len(files) > maxFilesAtOnce {
		log.Printf("Too many files in upload: %d (max %d)", len(files), maxFilesAtOnce)
		http.Error(w, fmt.Sprintf("Too many files. Maximum %d files at once", maxFilesAtOnce), http.StatusBadRequest)
		logRequest(r, http.StatusBadRequest)
		return
	}
	
	if len(files) == 0 {
		// Try single file field
		file, header, err := r.FormFile("file")
		if err != nil {
			log.Printf("No files provided in upload: %v", err)
			http.Error(w, "No files provided", http.StatusBadRequest)
			logRequest(r, http.StatusBadRequest)
			return
		}
		defer file.Close()

		newName := r.FormValue("newFileName")
		if newName == "" {
			newName = header.Filename
		}

		// Handle folder upload: if uploading to a specific path
		uploadPathParam := r.FormValue("path")
		if uploadPathParam != "" {
			// Upload to specific directory
			newName = filepath.Join(uploadPathParam, filepath.Base(newName))
		}

		log.Printf("Attempting to save single file: %s", newName)
		if err := saveUploadedFile(file, newName, header.Size, session.Username); err != nil {
			log.Printf("Failed to save file %s: %v", newName, err)
			http.Error(w, "Failed to save file: "+err.Error(), http.StatusInternalServerError)
			logRequest(r, http.StatusInternalServerError)
			return
		}

		log.Printf("File uploaded by %s: %s", session.Username, newName)
	} else {
		// Multiple files - handle folder uploads
		uploadPathParam := r.FormValue("path")
		successCount := 0
		failCount := 0
		
		for _, fileHeader := range files {
			file, err := fileHeader.Open()
			if err != nil {
				log.Printf("Error opening file %s: %v", fileHeader.Filename, err)
				failCount++
				continue
			}

			// Preserve folder structure from the original filename
			originalFilename := fileHeader.Filename
			
			// If uploading to a specific path, combine it with the original structure
			var targetFilename string
			if uploadPathParam != "" {
				// Upload to specific directory while preserving subfolder structure
				if strings.Contains(originalFilename, string(filepath.Separator)) {
					// File has subfolder structure, preserve it
					targetFilename = filepath.Join(uploadPathParam, originalFilename)
				} else {
					// Single file, upload to target directory
					targetFilename = filepath.Join(uploadPathParam, originalFilename)
				}
			} else {
				// Upload to root, preserve original structure
				targetFilename = originalFilename
			}

			log.Printf("Uploading file: %s -> %s", originalFilename, targetFilename)

			if err := saveUploadedFile(file, targetFilename, fileHeader.Size, session.Username); err != nil {
				log.Printf("Error saving file %s: %v", targetFilename, err)
				file.Close()
				failCount++
				continue
			}

			file.Close()
			log.Printf("File uploaded by %s: %s", session.Username, targetFilename)
			successCount++
		}
		
		log.Printf("Batch upload completed: %d successful, %d failed", successCount, failCount)
		if failCount > 0 {
			log.Printf("Warning: %d files failed to upload out of %d", failCount, successCount + failCount)
		}
	}

	path := r.URL.Query().Get("path")
	if path != "" {
		http.Redirect(w, r, "/?path="+path, http.StatusSeeOther)
	} else {
		http.Redirect(w, r, "/", http.StatusSeeOther)
	}
	logRequest(r, http.StatusSeeOther)
}

// Save uploaded file and update metadata
func saveUploadedFile(file io.Reader, filename string, size int64, username string) error {
	// Filename can be "folder/file.txt"
	// Validate path to prevent breakout
	cleanName := filepath.Clean(filename)
	cleanName = strings.TrimPrefix(cleanName, "/")
	cleanName = strings.TrimPrefix(cleanName, "./")
	if strings.Contains(cleanName, "..") {
		return fmt.Errorf("invalid filename")
	}

	filePath := filepath.Join(uploadPath, cleanName)

	// Ensure directory exists
	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

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
	// Create directory metadata for parents
	dirName := filepath.Dir(cleanName)
	if dirName != "." && dirName != "" && dirName != cleanName {
		parts := strings.Split(dirName, string(filepath.Separator))
		current := ""
		for _, part := range parts {
			if current == "" {
				current = part
			} else {
				current = filepath.Join(current, part)
			}
			if _, exists := fileMetadata[current]; !exists {
				fileMetadata[current] = FileInfo{
					Name:       part,
					UploadedBy: username,
					UploadedAt: time.Now(),
					Size:       0,
					IsDir:      true,
				}
			}
		}
	}

	// For metadata, we use the relative path as key
	fileMetadata[cleanName] = FileInfo{
		Name:       filepath.Base(cleanName),
		UploadedBy: username,
		UploadedAt: time.Now(),
		Size:       written,
		IsDir:      false,
	}
	metaMutex.Unlock()

	log.Printf("Saved file metadata: %s (size: %d)", cleanName, written)

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
	http.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))

	// Start server
	fmt.Printf("Server started at %s\n", serverPort)
	storageType := "Internal Storage"
	if strings.Contains(uploadPath, "/Android/data/") || strings.Contains(uploadPath, "external-1") {
		storageType = "Physical SD Card"
	} else if strings.HasPrefix(uploadPath, "/sdcard") || strings.HasPrefix(uploadPath, "/storage/emulated") {
		storageType = "Internal Shared Storage"
	}
	fmt.Printf("Files stored at: %s (%s)\n", uploadPath, storageType)
	log.Printf("Starting server on %s, storage: %s", serverPort, uploadPath)
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
