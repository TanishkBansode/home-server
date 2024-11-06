package main

import (
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

const uploadPath = "./public"

// Log incoming requests
func logRequest(r *http.Request, status int) {
	log.Printf("[%s] %s %s %d\n", time.Now().Format(time.RFC3339), r.Method, r.URL.Path, status)
}

func renderIndex(w http.ResponseWriter, r *http.Request) {

	tmpl, err := template.ParseFiles("templates/home.html")
	if err != nil {
		log.Printf("Error parsing home template: %v", err)
		http.Error(w, "Unable to load template", http.StatusInternalServerError)
		logRequest(r, http.StatusInternalServerError)
		return
	}

	tmpl.Execute(w, nil)
	logRequest(r, http.StatusOK)
}

func renderDocumentsPage(w http.ResponseWriter, r *http.Request) {
	files, err := listFiles(uploadPath)
	if err != nil {
		log.Printf("Error listing files for documents page: %v", err)
		http.Error(w, "Unable to list files", http.StatusInternalServerError)
		logRequest(r, http.StatusInternalServerError)
		return
	}

	tmpl, err := template.ParseFiles("templates/documents.html")
	if err != nil {
		log.Printf("Error parsing documents template: %v", err)
		http.Error(w, "Unable to load template", http.StatusInternalServerError)
		logRequest(r, http.StatusInternalServerError)
		return
	}

	tmpl.Execute(w, files)
	logRequest(r, http.StatusOK)
}

func listFiles(dir string) ([]string, error) {
	var files []string
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			files = append(files, entry.Name())
		}
	}
	return files, nil
}

func uploadFile(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Invalid request method", http.StatusMethodNotAllowed)
		logRequest(r, http.StatusMethodNotAllowed)
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		log.Printf("Error retrieving file from form: %v", err)
		http.Error(w, "Failed to get uploaded file", http.StatusBadRequest)
		logRequest(r, http.StatusBadRequest)
		return
	}
	defer file.Close()

	newName := r.FormValue("newFileName")
	if newName == "" {
		newName = header.Filename
	}

	filePath := filepath.Join(uploadPath, newName)
	dest, err := os.Create(filePath)
	if err != nil {
		log.Printf("Error creating file %s: %v", filePath, err)
		http.Error(w, "Unable to save the file", http.StatusInternalServerError)
		logRequest(r, http.StatusInternalServerError)
		return
	}
	defer dest.Close()

	_, err = io.Copy(dest, file)
	if err != nil {
		log.Printf("Error saving file %s: %v", filePath, err)
		http.Error(w, "Failed to save file", http.StatusInternalServerError)
		logRequest(r, http.StatusInternalServerError)
		return
	}

	log.Printf("File uploaded successfully: %s", newName)
	http.Redirect(w, r, "/documents", http.StatusSeeOther)
	logRequest(r, http.StatusSeeOther)
}

func renameFile(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Invalid request method", http.StatusMethodNotAllowed)
		logRequest(r, http.StatusMethodNotAllowed)
		return
	}

	oldName := r.FormValue("oldFileName")
	newName := r.FormValue("newFileName")

	if oldName == "" || newName == "" {
		log.Println("Rename request with empty file names")
		http.Error(w, "File names cannot be empty", http.StatusBadRequest)
		logRequest(r, http.StatusBadRequest)
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

	log.Printf("File renamed from %s to %s", oldName, newName)
	http.Redirect(w, r, "/documents", http.StatusSeeOther)
	logRequest(r, http.StatusSeeOther)
}

func downloadFile(w http.ResponseWriter, r *http.Request) {
	fileName := r.URL.Query().Get("file")
	if fileName == "" {
		log.Println("Download request without file specified")
		http.Error(w, "File not specified", http.StatusBadRequest)
		logRequest(r, http.StatusBadRequest)
		return
	}

	filePath := filepath.Join(uploadPath, fileName)
	w.Header().Set("Content-Disposition", "attachment; filename="+fileName)
	http.ServeFile(w, r, filePath)
	log.Printf("File downloaded: %s", fileName)
	logRequest(r, http.StatusOK)
}

func main() {
	// Ensure the upload directory exists
	os.MkdirAll(uploadPath, os.ModePerm)

	// Set up handlers
	http.HandleFunc("/", renderIndex)
	http.HandleFunc("/documents", renderDocumentsPage)
	http.HandleFunc("/upload", uploadFile)
	http.HandleFunc("/rename", renameFile)
	http.HandleFunc("/download", downloadFile)
	http.Handle("/public/", http.StripPrefix("/public/", http.FileServer(http.Dir(uploadPath))))

	// Start server with logging
	fmt.Println("Server started at :8080")
	log.Println("Starting server on :8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
