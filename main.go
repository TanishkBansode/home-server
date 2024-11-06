package main

import (
    "fmt"
    "html/template"
    "io"
    "log"
    "net/http"
    "os"
    "path/filepath"
)

const uploadPath = "./public"

func renderIndex(w http.ResponseWriter, r *http.Request) {
    tmpl, _ := template.ParseFiles("templates/home.html")
    tmpl.Execute(w, files)
}

func renderDocumentsPage(w http.ResponseWriter, r *http.Request) {
    files, err := listFiles(uploadPath)
    if err != nil {
        http.Error(w, "Unable to list files", http.StatusInternalServerError)
        return
    }

    tmpl, _ := template.ParseFiles("templates/documents.html")
    tmpl.Execute(w, files)
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
        return
    }

    file, header, err := r.FormFile("file")
    if err != nil {
        http.Error(w, "Failed to get uploaded file", http.StatusBadRequest)
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
        http.Error(w, "Unable to save the file", http.StatusInternalServerError)
        return
    }
    defer dest.Close()

    _, err = io.Copy(dest, file)
    if err != nil {
        http.Error(w, "Failed to save file", http.StatusInternalServerError)
        return
    }

    http.Redirect(w, r, "/documents", http.StatusSeeOther)
}

func renameFile(w http.ResponseWriter, r *http.Request) {
    if r.Method != "POST" {
        http.Error(w, "Invalid request method", http.StatusMethodNotAllowed)
        return
    }

    oldName := r.FormValue("oldFileName")
    newName := r.FormValue("newFileName")

    if oldName == "" || newName == "" {
        http.Error(w, "File names cannot be empty", http.StatusBadRequest)
        return
    }

    oldPath := filepath.Join(uploadPath, oldName)
    newPath := filepath.Join(uploadPath, newName)

    err := os.Rename(oldPath, newPath)
    if err != nil {
        http.Error(w, "Failed to rename file", http.StatusInternalServerError)
        return
    }

    http.Redirect(w, r, "/documents", http.StatusSeeOther)
}

func downloadFile(w http.ResponseWriter, r *http.Request) {
    fileName := r.URL.Query().Get("file")
    if fileName == "" {
        http.Error(w, "File not specified", http.StatusBadRequest)
        return
    }

    filePath := filepath.Join(uploadPath, fileName)
    w.Header().Set("Content-Disposition", "attachment; filename="+fileName)
    http.ServeFile(w, r, filePath)
}


func main() {
    os.MkdirAll(uploadPath, os.ModePerm)

    http.HandleFunc("/", renderIndex)
	http.HandleFunc("/documents", renderDocumentsPage)
    http.HandleFunc("/upload", uploadFile)
    http.HandleFunc("/rename", renameFile) 
    http.HandleFunc("/download", downloadFile)

 
    http.Handle("/public/", http.StripPrefix("/public/", http.FileServer(http.Dir(uploadPath))))

    fmt.Println("Server started at :8080")
    log.Fatal(http.ListenAndServe(":8080", nil))
}
